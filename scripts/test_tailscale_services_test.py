"""Local harness tests; these do not establish Tailscale Services acceptance."""
import importlib.util
import contextlib
import io
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('services_proof', ROOT/'test-tailscale-services.py')
proof = importlib.util.module_from_spec(spec)
spec.loader.exec_module(proof)
SCRATCH = str(proof.scratch_root())
Path(SCRATCH).mkdir(parents=True, exist_ok=True)
os.environ.update(TMPDIR=SCRATCH, TMP=SCRATCH, TEMP=SCRATCH)


class HarnessTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix='rementor-provider-test-', dir=SCRATCH)
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.gateway = proof.Gateway('a', 'local:'+str(self.root/'tailscaled.sock'))
        self.gateway.baseline = {}
        self.addCleanup(self.gateway.cleanup, 'svc:rementor-proof')

    def test_gateway_uses_the_configured_scratch_directory(self):
        alternate = self.root/'runner-temp'
        with patch.dict(os.environ, {'TMPDIR':str(alternate)}):
            proof.Gateway('a', 'local:'+str(alternate/'task'/'tailscaled.sock'))
            for socket in [self.root/'outside.sock', alternate/'..'/'escape.sock']:
                with self.assertRaises(proof.ProofError):
                    proof.Gateway('a', 'local:'+str(socket))

    def test_backend_shutdown_is_acknowledged_and_port_closes(self):
        port = self.gateway.start_backend()
        with socket.create_connection(('127.0.0.1', port), timeout=1):
            pass
        self.assertEqual(self.gateway.cleanup('svc:rementor-proof'), [])
        with self.assertRaises(OSError):
            socket.create_connection(('127.0.0.1', port), timeout=1)
        self.gateway.backend = None

    def test_backend_startup_has_a_deadline(self):
        with patch.object(self.gateway, 'command', return_value=['python3', '-c', 'import sys; sys.stdin.readline()']):
            with self.assertRaisesRegex(proof.ProofError, 'startup timed out'):
                self.gateway.start_backend(timeout=.05)
        errors = self.gateway.cleanup('svc:rementor-proof')
        self.assertTrue(any('shutdown was not confirmed' in error for error in errors))
        self.assertIsNotNone(self.gateway.backend.poll())
        self.gateway.backend = None

    def test_eof_stops_backend(self):
        self.gateway.start_backend()
        self.gateway.backend.stdin.close()
        self.gateway.backend.stdin = None
        output, _ = self.gateway.backend.communicate(timeout=3)
        self.assertIn(b'"stopped": true', output)
        self.assertEqual(self.gateway.backend.returncode, 0)
        self.gateway.backend = None

    def test_cleanup_stops_backend_when_service_cleanup_fails(self):
        self.gateway.start_backend()
        self.gateway.configured = True
        with patch.object(self.gateway, 'tailscale', side_effect=proof.ProofError('provider clear failed')):
            self.assertEqual(self.gateway.cleanup('svc:rementor-proof'), ['provider clear failed'])
        self.assertEqual(self.gateway.backend.returncode, 0)
        self.gateway.backend = None
        self.gateway.configured = False

    def test_preflight_rejects_existing_user_node_and_test_service(self):
        status = {'BackendState':'Running', 'Self':{'ID':'n-proof', 'HostName':'marcus'}}
        with patch.object(self.gateway, 'tailscale', return_value=json.dumps(status)) as call:
            with self.assertRaisesRegex(proof.ProofError, 'dedicated tagged'):
                self.gateway.preflight('svc:rementor-proof')
            self.assertEqual(call.call_count, 1)
        status['Self'].update(HostName='rementor-services-proof-a', Tags=['tag:proof'])
        with patch.object(self.gateway, 'tailscale', side_effect=[json.dumps(status), json.dumps({'Services':{'svc:rementor-proof':{}}})]):
            with self.assertRaisesRegex(proof.ProofError, 'already has configuration'):
                self.gateway.preflight('svc:rementor-proof')

    def test_default_preflight_never_starts_backends_or_advertises(self):
        def preflight(gateway, service):
            gateway.physical_host = gateway.label
            return 'node-'+gateway.label
        argv = ['test-tailscale-services.py','--gateway-a','local:'+str(self.root/'a.sock'),
                '--gateway-b','brutus-raw:'+str(self.root/'b.sock')]
        with patch('sys.argv', argv), patch.object(proof.Gateway,'preflight',new=preflight), \
             patch.object(proof,'service_hostname',return_value='rementor-proof.fixture.ts.net'), \
             patch.object(proof.Gateway,'configure') as configure, \
             patch.object(proof.subprocess,'Popen') as process, contextlib.redirect_stdout(io.StringIO()):
            proof.main()
            configure.assert_not_called()
            process.assert_not_called()

    def test_service_catalog_discovery_can_wait_for_advertisement(self):
        replies = [b'[]', b'[{"Hostname":"rementor-proof.fixture.ts.net"}]']
        with patch.object(proof.subprocess,'check_output',side_effect=replies), patch.object(proof.time,'sleep'):
            self.assertEqual(proof.service_hostname('rementor-proof',timeout=1), 'rementor-proof.fixture.ts.net')

    @unittest.skipUnless(shutil.which('nginx') and shutil.which('openssl'), 'requires nginx and openssl')
    def test_tls_cookie_websocket_probes_and_forgery_detection(self):
        # Simulate only Serve's documented HTTP boundary. The physical Service
        # proof must still establish that the real provider has this behavior.
        fake_tailscale = self.root/'tailscale'
        fake_tailscale.write_text('#!/usr/bin/env python3\nimport json\nprint(json.dumps({"Node":{"StableID":"fixture-node"}}))\n')
        fake_tailscale.chmod(0o755)
        with patch.dict(os.environ, {'PATH':str(self.root)+os.pathsep+os.environ['PATH']}):
            port = self.gateway.start_backend()
        cert, key = self.root/'cert.pem', self.root/'key.pem'
        subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(key),'-out',str(cert),'-days','1','-subj','/CN=localhost','-addext','subjectAltName=DNS:localhost'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        listener = socket.socket()
        listener.bind(('127.0.0.1', 0))
        tls_port = listener.getsockname()[1]
        listener.close()
        config = self.root/'nginx.conf'
        config.write_text(f'''pid {self.root}/nginx.pid;
error_log {self.root}/error.log;
events {{}}
http {{
 access_log off;
 server {{
  listen 127.0.0.1:{tls_port} ssl;
  ssl_certificate {cert};
  ssl_certificate_key {key};
  location / {{
   proxy_pass http://127.0.0.1:{port};
   proxy_http_version 1.1;
   proxy_set_header Upgrade $http_upgrade;
   proxy_set_header Connection upgrade;
   proxy_set_header Host $http_host;
   proxy_set_header X-Forwarded-For 100.64.0.10;
   proxy_set_header X-Forwarded-Proto https;
   proxy_set_header Tailscale-User-Login "";
  }}
 }}
}}
''')
        nginx = subprocess.Popen(['nginx','-p',str(self.root),'-c',str(config),'-g','daemon off;'], stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        def stop_nginx():
            nginx.terminate()
            nginx.wait(timeout=5)
        self.addCleanup(stop_nginx)
        # await_gateway retries listener startup while retaining full TLS checks.
        url = f'https://localhost:{tls_port}/probe'
        identity = {'nodeId':'fixture-node','sources':['100.64.0.10']}
        with patch.dict(os.environ, {'SSL_CERT_FILE':str(cert)}):
            proof.await_gateway('local', url, 'a', identity, 5)
            proof.await_gateway('local', url, 'a', identity, 5, websocket=True)
            result = proof.probe('local', url)
            self.assertTrue(result['cookiesVerified'])
            result['forgedUserSurvived'] = True
            with patch.object(proof, 'probe', return_value=result):
                with self.assertRaisesRegex(proof.ProofError, 'forged provider header'):
                    proof.await_gateway('local', url, 'a', identity, 1)
            # A certificate trusted for localhost still cannot authenticate an IP.
            with self.assertRaisesRegex(proof.ProofError, 'HTTPS probe failed'):
                proof.probe('local', url.replace('localhost', '127.0.0.1'))


if __name__ == '__main__':
    unittest.main()
