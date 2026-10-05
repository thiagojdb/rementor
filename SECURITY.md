# Security Model

Rementor’s management API is a local developer interface. Computer pairing uses
a separate, opt-in authenticated peer listener.

## Local Control Plane

The server accepts loopback bind addresses only. It refuses `0.0.0.0`, public
interfaces, and hostnames that are not `localhost` because the control plane is
not designed as a remotely authenticated service.

Browser RPC calls include a CSRF guard. CLI and MCP calls use the local Connect
RPC API directly.

The container demo publishes port `8080` to `127.0.0.1` by default. Setting
`REMENTOR_DEMO_HOST=0.0.0.0` or using `docker run -p 0.0.0.0:8080:8080`
intentionally exposes the demo to other machines and should only be used on
trusted networks.

Generated proxy routes include CORS headers for local development origins only:
`localhost`, `127.0.0.1`, `::1`, and `*.localhost`. They do not intentionally
allow arbitrary public origins to read responses from services reachable through
Rementor.

Route proof headers use the `X-Rementor-*` namespace. Generated locations hide
upstream copies before adding their own values, and incoming correlation IDs are
validated or replaced with a generated request ID before being forwarded.

## Computer Pairing

Creating an invitation or confirming a pairing enables the peer connection.
Automatic setup prefers a Tailscale address, then a private network interface,
with port 9443 or the next free port through 9458. It skips container bridges
and loopback-only addresses. Connection settings can select a specific loopback,
LAN, or Tailscale IP address instead. Wildcard and public bind addresses are rejected.
The peer listener does not serve the UI or management RPCs. Paired computers
can fetch and edit source environment configuration and forward
traffic to declared local applications.

Create an invitation on one computer and paste it into Connect to computer on the other.
Compare the SHA-256 certificate fingerprint against the source computer before
confirming. An invitation carries a cryptographically random 256-bit enrollment
secret, the peer address, and its certificate fingerprint. It expires after five
minutes, is consumed once, and can be cancelled. Creating another invitation
invalidates the previous one. Restarting or disabling the listener cancels it.
Anyone holding the invitation can enroll during that window; share it directly
through a trusted channel. An invitation never contains a private key.

The initiator pins the server certificate before sending the secret. TLS 1.3
requires proof of the client’s private key. Both computers persist the other’s
certificate fingerprint, and every subsequent connection checks the pinned
server certificate and the current server-side trust list. Public CA certificates
are not required for this interface, and HTTP redirects are rejected.

Disconnect revokes trust locally, including requests on existing TLS connections.
It also removes the address used for outgoing checks. The other computer keeps
its own trust record until disconnected there; an authenticated check will report
that the relationship no longer works. Disabling incoming connections preserves
saved trust records. Pairing authorizes connection checks, configuration snapshots, application
forwarding, and typed source-configuration edits (route selections, environment
routing settings, service definitions, registrations, and deletion). Source edits require the expected source route version and
commit with routing validation, persistence, and rollback. Paired computers cannot
execute commands or call arbitrary management RPCs. Registration changes apply
only in the source computer’s network namespace. Workspace intent includes routing hostnames, remote addresses, local
ports, and the selected environment/session; share invitations only with trusted
computers. Snapshot fetches recheck trust after exporting and after receiving a
response, so revocation during a fetch rejects the candidate.

Forwarding requires a binding ID, workspace, owner fingerprint, route version,
and content digest from a validated snapshot. The owner resolves the binding to
its own loopback port; callers cannot supply an arbitrary URL or port. Changed
snapshots are rejected with HTTP 409, and unavailable targets fail without
fallback. The management port, peer listener, application gateway, and local
management ingress ports cannot be forwarded even
when registered as an application. Declared registrations can be selected by a
paired receiving instance independently of the owner's local/remote toggle.
Public host, Origin, authorization, cookies,
paths, queries, bodies, streaming responses, and WebSocket upgrades are relayed.
Peer metadata and route proof headers are stripped from application requests;
endpoint response proofs are generated from the verified reference. Revocation,
disable, and listener shutdown cancel active streams and upgraded connections.
Nginx sends peer endpoint references to a separate loopback-only application
gateway. It exposes no management or enrollment handlers, validates the
reference, and uses the current peer trust before forwarding. Nginx emits the
selected endpoint's owner/version/digest even on errors and hides upstream
attempts to forge those headers. Synchronization commits under a current-trust
guard, serialized with revocation and disable. Persisted caches are revalidated
against their identity, scope, schema and digest before use. A source outage keeps
the last validated choice with stale source status; it does not select another
application target. Endpoint probes use the same authenticated forwarding boundary.

Identity keys and trust records live in `peers/identity.json` under the data
directory, with file mode `0600` and directory mode `0700`. They survive restarts.
Do not copy this identity onto another computer. Local users who can read the key
can impersonate that Rementor installation. Network access through Tailscale or
another VPN does not replace Rementor’s certificate and invitation checks.

## Local Data

Rementor stores workspace definitions, application metadata, routing state, and
route patterns in SQLite under the XDG data directory:

```text
~/.local/share/rementor/rementor.db
```

Rementor creates its app-specific XDG data, cache, and config directories with
`0700` permissions and creates the SQLite database with `0600` permissions.
Treat the database as sensitive and do not copy it into bug reports.

## System Changes

The container demo runs nginx and Rementor as an unprivileged user. Rementor
does not install system nginx configuration, edit `/etc/hosts`, create DNS
services, or install sudoers rules.

For a manually managed nginx instance, review the generated files under
`~/.config/rementor/nginx/` before including that directory. Do not make a
root-owned nginx master load configuration from a directory writable by
untrusted users.

## Reporting

Do not open a public issue containing a credential, private URL, local database,
or sensitive log. Use GitHub's private vulnerability reporting feature for
security reports.

## Application ingress

The public application listener is separate from management and peer enrollment,
and binds only to loopback. Its current provider boundary is Tailscale HTTPS
Serve. It requires exactly one Serve-generated source IP and HTTPS protocol
header, then resolves that exact address through the local tailscaled Unix
socket. It checks authorized/non-expired node identity and source-address
ownership; user-login headers are not used as device identity. The provider proof
verified that Serve replaces forged and duplicate forwarding headers.

Accounts are explicitly allowed. User-owned device selections cannot bypass that
account list. Tagged nodes require explicit node selections and cannot acquire
shared access. Each selected workspace must bind every configured public origin.
Unknown origins, unpublished hosts, management hostnames/ports, and ambiguous
paths are rejected. Remote loopback URLs cannot be published as application
registrations. Peer targets retain the owning node's declared-port protections.

Ingress strips client-supplied Rementor/Tailscale/forwarding metadata and sets an
internal workspace/application/version guard. Nginx checks it before contacting
an application; ingress also verifies the response proof. A stale projection
fails with 409 instead of selecting a different target. Guard variables are
namespaced in the provider's per-workspace rendered fragments.

Credentialed CORS permits only configured origins in the selected scope and
suppresses upstream CORS headers. Cookies and redirects retain application
semantics; applications must use the configured public origins. Policy changes
use a persisted compare-and-swap revision and cancel existing HTTP/WebSocket
contexts. Management remains on loopback and browser mutations require CSRF.
Local processes remain within the same trust boundary as loopback management.

## Shared ingress

Shared ingress has an explicit gateway list, a selector owner, and a separate
routing computer. Enrollment in this group is a local management action; pairing
alone does not grant membership. Selector edits use record-version checks, and
handoff recovery/cancellation uses the local durable revision. Transfer preserves
the policy and routing computer. A prepared successor cannot act as selector
until it independently reads the predecessor's committed handoff. Offline replicas
cannot elect a replacement or edit the owner's policy.

Gateways and routing computers synchronize from the saved owner and fail if it
cannot be reached. Every shared request captures its group/digest and serving
role, and checks account/device authorization again at receiving computers. The
pinned gateway is trusted to report the original Serve source IP; each receiver
resolves that IP itself rather than accepting a supplied account identity. Shared
membership is therefore a trust grant to the enrolled gateway, not an isolation
boundary against a compromised paired gateway.

Owner, policy, and trust changes cancel the associated request scopes, including
upgraded connections. Shared response proofs are checked at each network hop;
upstream application copies cannot replace them. The routing computer retains
the existing nginx projection guard and protected-port checks. Shared peer
endpoints expose no local management RPC or arbitrary destination URL.
