#!/usr/bin/env python3
"""Prove Services DNS, TLS, source identity and movement on isolated gateways.

Requires an existing internal Tailscale Service and two tagged proof nodes.
Read-only preflight is the default; --run advertises only the named test service.
"""
import argparse
import json
import os
from pathlib import Path
import re
import selectors
import shlex
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

BACKEND = r'''
import base64, hashlib, http.server, ipaddress, json, subprocess, sys, threading
label, socket_path = sys.argv[1:]
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  sources = self.headers.get_all('X-Forwarded-For') or []
  protocols = self.headers.get_all('X-Forwarded-Proto') or []
  if not ipaddress.ip_address(self.client_address[0]).is_loopback or len(sources) != 1 or protocols != ['https']:
   self.send_error(403); return
  try:
   source = str(ipaddress.ip_address(sources[0]))
   who = json.loads(subprocess.check_output(['tailscale','--socket='+socket_path,'whois','--json',source], timeout=5, stderr=subprocess.DEVNULL))
   node_id = who['Node']['StableID']
   if not node_id: raise ValueError('missing identity')
  except Exception:
   self.send_error(403); return
  result = {'gateway':label,'source':source,'nodeId':node_id,'forgedUserSurvived':self.headers.get('Tailscale-User-Login') == 'forged.example','host':self.headers.get('Host'),'cookieReceived':'rementor-proof=synthetic-fixture' in self.headers.get('Cookie','')}
  if self.path == '/socket':
   key = self.headers.get('Sec-WebSocket-Key','')
   if self.headers.get('Upgrade','').lower() != 'websocket' or not key:
    self.send_error(400); return
   accept = base64.b64encode(hashlib.sha1((key+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
   self.send_response(101); self.send_header('Upgrade','websocket'); self.send_header('Connection','Upgrade'); self.send_header('Sec-WebSocket-Accept',accept); self.end_headers()
   self.connection.settimeout(5)
   frame = self.rfile.read(2)
   if len(frame) != 2 or frame[0] != 0x81 or not frame[1]&0x80 or frame[1]&0x7f >= 126: return
   mask = self.rfile.read(4)
   payload = self.rfile.read(frame[1]&0x7f)
   if len(mask)!=4 or len(payload)!=(frame[1]&0x7f): return
   result['echo'] = bytes(value^mask[index%4] for index,value in enumerate(payload)).decode()
   body = json.dumps(result).encode()
   header = bytes([0x81,len(body)]) if len(body)<126 else bytes([0x81,126])+len(body).to_bytes(2,'big')
   self.wfile.write(header+body); self.wfile.flush(); self.close_connection=True
   return
  body = json.dumps(result).encode()
  self.send_response(200); self.send_header('Content-Type','application/json'); self.send_header('Content-Length',str(len(body)))
  self.send_header('Set-Cookie','rementor-proof=synthetic-fixture; Secure; HttpOnly; SameSite=None; Path=/')
  self.end_headers(); self.wfile.write(body)
 def log_message(self, *args): pass
server = http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
server.daemon_threads = True
worker = threading.Thread(target=server.serve_forever, daemon=True)
worker.start()
print(json.dumps({'port':server.server_port}),flush=True)
# EOF also stops the remote process when its SSH control pipe disconnects.
try:
 sys.stdin.readline()
finally:
 server.shutdown()
 server.server_close()
 worker.join(timeout=2)
 print(json.dumps({'stopped':True}),flush=True)
'''

HTTP_PROBE = r'''
import http.cookiejar, json, sys, urllib.request
url=sys.argv[1]
jar=http.cookiejar.CookieJar()
opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
headers={'X-Forwarded-For':'100.127.255.254','X-Forwarded-Proto':'http','Tailscale-User-Login':'forged.example'}
for attempt in range(2):
 with opener.open(urllib.request.Request(url,headers=headers),timeout=3) as response:
  result=json.load(response)
cookies=list(jar)
result['cookiesVerified']=result['cookieReceived'] and len(cookies)==1 and cookies[0].secure and cookies[0].has_nonstandard_attr('HttpOnly') and cookies[0].get_nonstandard_attr('SameSite')=='None'
print(json.dumps(result))
'''

WEBSOCKET_PROBE = r'''
import base64, hashlib, json, os, socket, ssl, sys, urllib.parse
url=urllib.parse.urlparse(sys.argv[1])
key=base64.b64encode(os.urandom(16)).decode()
with socket.create_connection((url.hostname,url.port or 443),timeout=3) as raw:
 with ssl.create_default_context().wrap_socket(raw,server_hostname=url.hostname) as connection:
  connection.settimeout(3)
  request=('GET /socket HTTP/1.1\r\nHost: '+url.netloc+'\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: '+key+'\r\nX-Forwarded-For: 100.127.255.254\r\nX-Forwarded-Proto: http\r\nTailscale-User-Login: forged.example\r\nCookie: rementor-proof=synthetic-fixture\r\n\r\n')
  connection.sendall(request.encode())
  stream=connection.makefile('rb')
  status=stream.readline(4096)
  if status.split()[1:2]!=[b'101']: raise RuntimeError('upgrade rejected')
  headers={}
  for count in range(64):
   line=stream.readline(4096)
   if line==b'\r\n': break
   if not line or b':' not in line: raise RuntimeError('invalid upgrade response')
   name,value=line.split(b':',1);headers[name.lower()]=value.strip()
  else: raise RuntimeError('upgrade response too large')
  expected=base64.b64encode(hashlib.sha1((key+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest())
  if headers.get(b'sec-websocket-accept')!=expected: raise RuntimeError('invalid upgrade accept')
  message=b'rementor-proof-websocket';mask=os.urandom(4)
  payload=bytes(value^mask[index%4] for index,value in enumerate(message))
  connection.sendall(bytes([0x81,0x80|len(message)])+mask+payload)
  frame=stream.read(2)
  if len(frame)!=2 or frame[0]!=0x81 or frame[1]&0x80: raise RuntimeError('invalid reply frame')
  size=frame[1]&0x7f
  if size==126: size=int.from_bytes(stream.read(2),'big')
  if size>4096 or size==127: raise RuntimeError('reply too large')
  result=json.loads(stream.read(size))
  result['websocketVerified']=result.get('echo')==message.decode() and result['cookieReceived']
  print(json.dumps(result))
'''


class ProofError(Exception):
    pass


class Gateway:
    def __init__(self, label, specification):
        self.label = label
        try:
            self.host, self.socket = specification.split(':', 1)
        except ValueError as error:
            raise ProofError('Gateway must be HOST:SOCKET (HOST is local or an SSH profile)') from error
        if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]*', self.host):
            raise ProofError('Invalid SSH profile')
        if not self.socket.startswith('/home/thiago/.cache/ai-tmp/') or '..' in Path(self.socket).parts:
            raise ProofError('Proof sockets must be inside the task scratch directory')
        self.backend = None
        self.baseline = None
        self.configured = False
        self.offline = False
        self.physical_host = None

    def command(self, arguments):
        if self.host == 'local':
            return arguments
        return ['ssh', '-T', '-o', 'ConnectTimeout=8', self.host, shlex.join(arguments)]

    def invoke(self, arguments, timeout=20):
        result = subprocess.run(self.command(arguments), capture_output=True, timeout=timeout, text=True)
        if result.returncode:
            # Login links or credentials may occur in provider diagnostics.
            raise ProofError(f'{self.label}: {arguments[0]} failed (exit {result.returncode})')
        return result.stdout

    def tailscale(self, *arguments):
        return self.invoke(['tailscale', '--socket='+self.socket, *arguments])

    def preflight(self, service):
        status = json.loads(self.tailscale('status', '--json'))
        own = status.get('Self') or {}
        if status.get('BackendState') != 'Running':
            raise ProofError(f'{self.label}: isolated gateway needs Tailscale enrollment')
        if not own.get('Tags') or not own.get('HostName', '').startswith('rementor-services-proof-'):
            raise ProofError(f'{self.label}: require a dedicated tagged rementor-services-proof-* node')
        self.baseline = json.loads(self.tailscale('serve', 'status', '--json'))
        if service in json.dumps(self.baseline):
            raise ProofError(f'{self.label}: test service already has configuration; choose an unused service')
        self.physical_host = self.invoke(['python3', '-c', 'import socket; print(socket.gethostname())']).strip()
        return own.get('ID') or own.get('StableID')

    def start_backend(self, timeout=8):
        self.backend = subprocess.Popen(
            self.command(['python3', '-u', '-c', BACKEND, self.label, self.socket]),
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        # Bound startup even if SSH connects but never executes the backend.
        with selectors.DefaultSelector() as selector:
            selector.register(self.backend.stdout, selectors.EVENT_READ)
            deadline = time.monotonic()+timeout
            output = bytearray()
            while b'\n' not in output:
                remaining = deadline-time.monotonic()
                if remaining <= 0 or not selector.select(remaining):
                    raise ProofError(f'{self.label}: backend startup timed out')
                chunk = os.read(self.backend.stdout.fileno(), 4096)
                if not chunk or len(output)+len(chunk) > 4096:
                    raise ProofError(f'{self.label}: invalid backend startup response')
                output.extend(chunk)
        try:
            port = json.loads(output.split(b'\n', 1)[0])['port']
        except (ValueError, KeyError, TypeError) as error:
            raise ProofError(f'{self.label}: invalid backend startup response') from error
        if type(port) is not int or not 1 <= port <= 65535:
            raise ProofError(f'{self.label}: invalid backend port')
        return port

    def configure(self, service):
        port = self.start_backend()
        self.configured = True
        self.tailscale('serve', '--bg', '--yes', '--service='+service, '--https=443', 'http://127.0.0.1:'+str(port))

    def restore(self):
        if self.offline:
            self.tailscale('up')
            self.offline = False

    def cleanup(self, service):
        errors = []
        try:
            self.restore()
            if self.configured:
                self.tailscale('serve', 'clear', service)
                restored = json.loads(self.tailscale('serve', 'status', '--json'))
                if normalize(restored) != normalize(self.baseline):
                    raise ProofError(f'{self.label}: original Serve configuration was not restored')
        except Exception as error:
            errors.append(str(error))
        finally:
            if self.backend is not None:
                try:
                    output, _ = self.backend.communicate(b'stop\n', timeout=8)
                    if self.backend.returncode != 0 or not any(
                        json.loads(line).get('stopped') is True for line in output.splitlines()
                    ):
                        errors.append(f'{self.label}: backend shutdown was not confirmed')
                except (subprocess.TimeoutExpired, BrokenPipeError, ValueError) as error:
                    # Closing stdin is the remote shutdown protocol; killing
                    # the SSH client alone does not prove its child exited.
                    errors.append(f'{self.label}: backend shutdown failed ({type(error).__name__})')
                    self.backend.kill()
                    self.backend.communicate(timeout=5)
        return errors


def normalize(value):
    if value is None:
        return {}
    if isinstance(value, dict):
        return {key: normalize(item) for key, item in value.items() if item not in ({}, [], None)}
    return value


def addresses(hostname, host='local'):
    command = ['python3', '-c', 'import json,socket,sys; print(json.dumps(sorted({entry[4][0] for entry in socket.getaddrinfo(sys.argv[1],443,type=socket.SOCK_STREAM)})))', hostname]
    if host != 'local':
        command = ['ssh', '-T', '-o', 'ConnectTimeout=8', host, shlex.join(command)]
    result = subprocess.run(command, capture_output=True, timeout=15, text=True)
    if result.returncode:
        raise ProofError(f'{host}: Service DNS is unavailable')
    return json.loads(result.stdout)


def service_hostname(service, timeout=0):
    deadline = time.monotonic()+timeout
    while True:
        available = json.loads(subprocess.check_output(['tailscale', 'service', 'list', '--json'], timeout=15, stderr=subprocess.DEVNULL))
        entries = [entry for entry in available if entry.get('Hostname', '').startswith(service+'.')]
        if len(entries) == 1 and entries[0]['Hostname'].endswith('.ts.net'):
            return entries[0]['Hostname']
        if entries or time.monotonic() >= deadline:
            raise ProofError('Service is not visible; create the internal resource and approve both tagged gateways')
        time.sleep(.25)


def identity(host):
    command = ['tailscale', 'status', '--json']
    if host != 'local':
        command = ['ssh', '-T', '-o', 'ConnectTimeout=8', host, shlex.join(command)]
    status = json.loads(subprocess.check_output(command, timeout=15, stderr=subprocess.DEVNULL))
    own = status['Self']
    return {'nodeId': own.get('ID') or own.get('StableID'), 'sources': own['TailscaleIPs']}


def probe(host, url, websocket=False):
    # TLS verification and hostname checking stay enabled on both physical clients.
    command = ['python3', '-c', WEBSOCKET_PROBE if websocket else HTTP_PROBE, url]
    if host != 'local':
        command = ['ssh', '-T', '-o', 'ConnectTimeout=8', host, shlex.join(command)]
    result = subprocess.run(command, capture_output=True, timeout=15, text=True)
    if result.returncode:
        raise ProofError(f'{host}: {"WebSocket" if websocket else "HTTPS"} probe failed')
    return json.loads(result.stdout)


def await_gateway(host, url, expected_gateway, expected_identity, timeout, websocket=False):
    started = time.monotonic()
    while time.monotonic() - started < timeout:
        try:
            result = probe(host, url, websocket)
            if result['gateway'] != expected_gateway:
                time.sleep(0.25)
                continue
            if result['nodeId'] != expected_identity['nodeId'] or result['source'] not in expected_identity['sources'] or result['forgedUserSurvived']:
                raise ProofError('Source identity was lost or a forged provider header survived')
            if result['host'] != url.split('/')[2]:
                raise ProofError('Public Host changed across the provider')
            if not result.get('websocketVerified' if websocket else 'cookiesVerified'):
                raise ProofError('WebSocket or secure cookie forwarding failed')
            return round(time.monotonic()-started, 3)
        except (subprocess.TimeoutExpired, urllib.error.URLError, json.JSONDecodeError):
            time.sleep(0.25)
        except ProofError as error:
            if 'probe failed' not in str(error):
                raise
            time.sleep(0.25)
    raise ProofError(f'{host}: gateway {expected_gateway} did not serve the stable URL within {timeout}s')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--gateway-a', required=True, help='local:/home/thiago/.cache/ai-tmp/TASK/tailscaled.sock')
    parser.add_argument('--gateway-b', required=True, help='brutus-raw:/home/thiago/.cache/ai-tmp/TASK/tailscaled.sock')
    parser.add_argument('--service', default='rementor-proof', help='Existing internal Service name, without svc:')
    parser.add_argument('--run', action='store_true', help='Advertise test endpoints, drain each gateway and simulate an outage')
    parser.add_argument('--timeout', type=int, default=45, help='Seconds allowed for service convergence')
    args = parser.parse_args()
    if not re.fullmatch(r'rementor-proof(?:-[a-z0-9-]+)?', args.service):
        raise ProofError('Use a dedicated rementor-proof service name')
    if not 5 <= args.timeout <= 120:
        raise ProofError('Timeout must be between 5 and 120 seconds')
    gateways = [Gateway('a', args.gateway_a), Gateway('b', args.gateway_b)]
    if gateways[0].host != 'local' or gateways[1].host == 'local':
        raise ProofError('The proof requires separate local and remote physical hosts')
    service = 'svc:'+args.service
    nodes = [gateway.preflight(service) for gateway in gateways]
    if not nodes[0] or nodes[0] == nodes[1]:
        raise ProofError('The proof requires two distinct tagged identities')
    if not gateways[0].physical_host or gateways[0].physical_host == gateways[1].physical_host:
        raise ProofError('The proof requires gateways on different physical computers')
    # Catalog visibility may require an active, approved advertisement. Discover
    # the actual provider hostname after advertising; never guess a DNS suffix.
    if not args.run:
        hostname = service_hostname(args.service)
        print(json.dumps({'preflight':'passed','hostname':hostname,'physicalGateways':2,'taggedIdentities':2}), flush=True)
        return
    os.environ.update(TMPDIR='/home/thiago/.cache/ai-tmp', TMP='/home/thiago/.cache/ai-tmp', TEMP='/home/thiago/.cache/ai-tmp')
    scratch = Path(tempfile.mkdtemp(prefix='rementor-services-proof-', dir='/home/thiago/.cache/ai-tmp'))
    identities = {host: identity(host) for host in ['local', gateways[1].host]}
    results = {'movement':[],'passed':False}
    failure = None
    try:
        for gateway in gateways:
            gateway.configure(service)
        hostname = service_hostname(args.service, args.timeout)
        url = 'https://'+hostname+'/probe'
        dns = {host: addresses(hostname, host) for host in identities}
        if len({tuple(value) for value in dns.values()}) != 1:
            raise ProofError('Physical clients resolve different Service addresses')
        results.update(hostname=hostname, dns=dns)
        for drained, survivor in [(gateways[0], gateways[1]), (gateways[1], gateways[0])]:
            drained.tailscale('serve', 'drain', service)
            for host, expected in identities.items():
                elapsed = await_gateway(host, url, survivor.label, expected, args.timeout)
                ws_elapsed = await_gateway(host, url, survivor.label, expected, args.timeout, websocket=True)
                results['movement'].append({'action':'drain','gateway':drained.label,'client':host,'seconds':elapsed,'websocketSeconds':ws_elapsed})
            if any(addresses(hostname, host) != dns[host] for host in identities):
                raise ProofError('Service DNS changed when the gateway moved')
            drained.tailscale('serve', 'advertise', service)
        gateways[0].offline = True
        gateways[0].tailscale('down')
        for host, expected in identities.items():
            elapsed = await_gateway(host, url, gateways[1].label, expected, args.timeout)
            ws_elapsed = await_gateway(host, url, gateways[1].label, expected, args.timeout, websocket=True)
            results['movement'].append({'action':'network-outage','gateway':'a','client':host,'seconds':elapsed,'websocketSeconds':ws_elapsed})
        gateways[0].restore()
        if any(addresses(hostname, host) != dns[host] for host in identities):
            raise ProofError('Service DNS changed during the outage')
        results.update(tlsVerified=True, sourceIdentityPreserved=True, forgedHeadersRejected=True, secureCookiesVerified=True, websocketsVerified=True, passed=True)
    except Exception as error:
        failure = error
        results['error'] = str(error)
    finally:
        cleanup_errors = [error for gateway in gateways for error in gateway.cleanup(service)]
        results['originalServeConfigurationRestored'] = not cleanup_errors
        if cleanup_errors:
            results.update(passed=False, cleanupErrors=cleanup_errors)
        (scratch/'receipt.json').write_text(json.dumps(results, indent=2)+'\n')
        print(json.dumps({'passed':results['passed'],'receipt':str(scratch/'receipt.json')}), flush=True)
    if failure or cleanup_errors:
        raise ProofError('Services proof failed; inspect the receipt before retrying')


if __name__ == '__main__':
    try:
        main()
    except (ProofError, subprocess.TimeoutExpired) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
