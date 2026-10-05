# Rementor

**A local control plane for developing one microservice against the rest of a
remote stack.**

Rementor lets a developer run only the service they are changing, route selected
traffic to it, and leave every other dependency on a shared environment. A web
UI, CLI, and MCP server all use the same typed control-plane API.

![Rementor dashboard showing a sanitized mock workspace](docs/assets/dashboard.png)

- Switch individual paths or hostnames between local ports and remote upstreams.
- Compare local and remote health without starting the full service graph.
- Generate and validate nginx routes from persisted workspace configuration.
- Control routing from a SolidJS UI, `rementorctl`, or coding-agent MCP tools.
- Keep the workflow reproducible with SQLite state, Protocol Buffers, and a
  rootless mock demo.

## Why It Exists

In a large microservice system, running the entire stack locally is often slow or
impossible. A typical feature needs one local service plus authentication,
frontends, APIs, and data services that already exist in a shared environment.

Rementor provides a stable local URL while choosing, per application, whether
requests go to local code or a remote baseline. This shortens the feedback loop
for feature work and makes distributed routing failures visible.

## Try It

The demo contains only neutral mock services and loopback addresses. It does not
need credentials, private DNS, or access to a real environment.

Prerequisites: Docker with Compose.

```bash
docker compose up --build
```

Open <http://localhost:8080>. The demo starts:

| Component | Address |
|---|---|
| Rementor UI and reverse proxy | `http://localhost:8080` |
| Remote mock stack | internal `127.0.0.1:18080` |
| Local mock services | internal `127.0.0.1:28080-28082` |

The `demo` workspace is registered automatically. Toggle `orders-api` in the UI,
then compare the routed response in a browser:

```text
http://api.localhost:8080/orders
```

Or use curl against the published localhost port with an explicit Host header:

```bash
curl -H 'Host: api.localhost' http://localhost:8080/orders
```

The demo applications are `web`, `orders-api`, and `billing-api`. The canonical
paths to compare are `/` for `web`, `/orders`, and `/billing`.

Without Docker Compose, the same image can be run directly:

```bash
docker build -t rementor-demo .
docker run --rm -p 127.0.0.1:8080:8080 rementor-demo
```

The demo port is bound to loopback by default. To intentionally expose it on
another interface, pass the bind host explicitly:

```bash
REMENTOR_DEMO_HOST=0.0.0.0 docker compose up --build
docker run --rm -p 0.0.0.0:8080:8080 rementor-demo
```

The same `build` and `run` commands also work with Podman.

Only expose the demo on trusted networks. The control plane is designed for
local development, not as a remotely authenticated service.

## Workflow

```mermaid
flowchart LR
  Client[Browser or API client] --> Proxy[nginx on a stable local URL]
  Proxy -->|selected service| Local[Local process]
  Proxy -->|remaining services| Remote[Remote environment]
  UI[SolidJS UI] --> RPC[Connect RPC control plane]
  CLI[rementorctl] --> RPC
  MCP[MCP tools] --> RPC
  RPC --> Registry[Workspace registry]
  Registry --> DB[(SQLite)]
  Registry --> Proxy
  Registry --> Health[Local and remote health checks]
```

1. Define a workspace for a remote environment.
2. Register applications with their route, context, local port, and health path.
3. Start one application locally.
4. Toggle that application to local; all other routes remain remote.
5. Toggle it back to compare behavior against the remote baseline.

Routing changes are applied before state is persisted. If nginx validation or
reload fails, Rementor restores the previous in-memory and persisted state.

## CLI Example

Start the server and frontend for development:

```bash
make setup
make dev
```

For an installed user service, open the Rementor UI at
<http://rementor.localhost/> through nginx. If nginx has not been reloaded yet,
use the direct fallback <http://localhost:9300>. Generated nginx routes also
listen on loopback port `80`, so configured local domains such as
`http://api.localhost/` do not require a port. The Docker demo remains on
`8080` because it runs nginx without root privileges.

In another terminal:

```bash
go run ./examples/mock-stack
make build-ctl

./dist/rementorctl workspace create demo \
  --name "Demo Workspace" \
  --local-domain api.localhost \
  --default-remote-base-url http://127.0.0.1:18080

./dist/rementorctl app register demo orders-api \
  --name "Orders API" \
  --port 28081 \
  --path /orders \
  --context /orders \
  --health health

./dist/rementorctl app toggle demo orders-api
./dist/rementorctl --json app list demo

# Inspect, plan, apply, and verify normalized routes.
./dist/rementorctl route get demo
./dist/rementorctl route conflicts --workspace demo
./dist/rementorctl route resolve demo --host api.localhost --path /orders/42
./dist/rementorctl route plan demo orders-api --mode local
./dist/rementorctl route apply demo orders-api --mode local --idempotency-key orders-local
./dist/rementorctl route sync demo

# Resolve the stable browser entry point (aliases are accepted).
./dist/rementorctl url --workspace demo --app orders-api
```

Flags may appear before or after positional arguments.

## Parallel feature sessions

Create an isolated routing session under an existing routing environment. Its
hostname prepends the session name to the environment hostname: for example,
`feature-x.desenvolvimento.giss.localhost`. An application that has its own
hostname follows the same rule, such as `feature-x.dev.giss.localhost`. Sessions
start with a pinned copy of the environment's remote routes; general-development
local ports and toggles are not inherited.

In the dashboard, open an environment and choose **New session**. In that
session, select a service's local-port value to register a port, then use its
**Use local** switch. The session action menu lets you review and apply a remote
baseline refresh while preserving session customizations. The sidebar lists
**General routing** and the environment's sessions as branches; selecting the
environment returns to general routing. Sessions persist until **Close session**
removes their routes.

The CLI supports the same workflow:

```bash
rementorctl session create feature-x --workspace demo --json
# Use the returned id for subsequent commands.
rementorctl app register demo orders-api --session <session-id> --port 24001
rementorctl app toggle demo orders-api --session <session-id>
rementorctl url --workspace demo --app orders-api --session <session-id>
rementorctl session list --workspace demo
rementorctl session inspect <session-id>
rementorctl session refresh <session-id> --json
# Apply the reviewed preview with its returned previewToken:
rementorctl session refresh <session-id> --apply --preview-token <token>
rementorctl session close <session-id>
```

`REMENTOR_SESSION` provides a per-process CLI/MCP default; `--session` or the MCP
`session` argument overrides it. The workspace must identify the session's parent
environment (or the session itself). A mismatched or closed selector fails rather
than changing general routing. There is no machine-global current session.

MCP exposes `rementor.session_create`, `rementor.session_list`,
`rementor.session_inspect`, `rementor.session_refresh`, and
`rementor.session_close`. Existing application, route, and URL tools accept a
`session` argument. The protobuf requests use the additive `session_id` field;
session projections retain an explicit parent environment and session identity.
The session ID is also a routing scope ID usable by existing workspace operations.

Port values are explicit configuration for the local process. Rementor does not
allocate ports, create Git worktrees, or start processes. Registration preserves
the current mode; new registrations start remote. Health does not gate toggling:
a selected but unavailable local endpoint returns the applicable nginx/upstream
error. Two sessions may register the same local process; application details
report other registrations sharing its port.

Browser API and websocket requests must use the session origin. Relative URLs
work when frontend and APIs share an origin. A hardcoded general-development API
URL escapes the session. Remote databases and other remote side effects remain
shared. Per-application frontend domains keep their parent hostname with the
session prefix; use the URL resolver instead of constructing those addresses.

When exposing a proxy on a nonstandard public port, set `REMENTOR_PUBLIC_PORT`
on the daemon so returned browser URLs include it. The server's `-port` controls
the control plane, including generated dashboard/trace upstreams; it is distinct
from both the public proxy port and application ports.

## Architecture

| Area | Implementation |
|---|---|
| Control plane | Go, Echo, Connect RPC |
| API contract | Protocol Buffers, Buf, generated Go and TypeScript clients |
| Frontend | React, TypeScript, Vite, Tailwind CSS |
| Persistence | SQLite under XDG data directories |
| Routing | Validated nginx configuration with atomic replacement and rollback |
| Automation | CLI plus MCP tools backed by the generated Connect client |
| Observability | Local/remote health checks and per-client streaming updates |

SQLite stores durable workspace configuration and route state. The daemon keeps
an in-memory runtime projection for low-latency routing operations, health
checks, connected UI streams, and routing-provider state. Mutations build and
validate a detached candidate, apply nginx routing from it, persist the durable
state, then publish it to the runtime projection. A persistence failure triggers
a compensating nginx reload from the previous snapshot.

Key directories:

```text
proto/rementor/v1/       Protocol Buffers API contract
internal/rpc/            Connect RPC service and CSRF guard
internal/services/       Registry, health checks, and routing transactions
internal/nginx/          nginx renderer, validation, and atomic reload
internal/config/         SQLite persistence and migrations
internal/cli/            CLI and MCP control surfaces
web/frontend/            React/shadcn application and generated TypeScript client
examples/mock-stack/     Sanitized local/remote service simulation
examples/docker/         Rootless nginx demo runtime
```

The Configuration page (`/configuration`) pairs Rementor computers:

1. On the computer you want to add, choose **Invite computer**.
2. On this computer, choose **Connect to computer**, paste the code,
   compare the fingerprint with the other computer, and confirm pairing.
3. Use **Check connection** to verify access, or **Disconnect** to revoke local trust.

Inviting or confirming a pairing enables the private connection automatically.
Rementor prefers Tailscale, then a private network address. **Connection settings**
lets you change the address or port; allow the selected port through your firewall
if your private network requires it.

You can also generate an invitation on the inviting computer from the terminal:

```bash
rementorctl peer invite
# For a custom management port:
rementorctl --server http://localhost:9400 peer invite
```

The command prints the invitation to stdout and its fingerprint and expiry to
stderr. Paste it into **Connect to computer** on the other computer and compare
fingerprints. `--json` returns all three fields together. This command also enables
the private connection when needed, using the running instance's existing identity.

Invitations expire after five minutes and work once. Peer connections use pinned
certificates and TLS client authentication; the management API stays on localhost.
See [the pairing security model](SECURITY.md#computer-pairing).

The route-plan API and [fleet inspector](examples/fleet/README.md) remain
experimental. The nginx gateway can forward declared peer endpoints through the
authenticated owner, including WebSockets. The backend persists live inheritance,
cached snapshots and whole-computer following. UI, CLI and MCP controls select
a source computer and edit its route selections; pairing authorizes the connection.

The saved fleet topology has separate review and save commands:

```bash
rementorctl fleet show
rementorctl fleet review --file topology.json --expected-revision 0
rementorctl fleet save --file topology.json --expected-revision 0 --review-token <reviewToken>
```

Edit the `configuration.topology` object returned by `fleet show`. Review returns
before/after selections and a token bound to that exact topology and revision.
Changing the file, another writer saving, or restarting the daemon requires a new
review. MCP exposes `rementor.fleet_show`, `rementor.fleet_review`, and
`rementor.fleet_save`; revisions are decimal strings. This topology stores intent;
the workspace inheritance controls change active proxy routes.

## MCP compatibility

`rementorctl mcp` supports both the handshake-based MCP `2024-11-05` protocol
and the stateless MCP `2026-07-28` protocol. The default `auto` mode selects the
protocol from the harness's first protocol-bearing request:

```bash
rementorctl mcp
rementorctl mcp --protocol modern
rementorctl mcp --protocol legacy
```

Use an explicit mode only when a harness cannot negotiate correctly. Modern
requests carry their protocol version and client capabilities in `_meta` on
every request; legacy clients continue to use `initialize` and
`notifications/initialized`. Both modes expose the same Rementor tools.

## nginx Integration

Rementor runs without nginx for the UI, CLI, MCP, configuration, and health
features. On a host with nginx installed, `make install` creates a private,
unprivileged nginx instance on `127.0.0.1:18080`. Point an existing system
reverse proxy at that upstream to expose Rementor's local domains on port 80.

For manually managed nginx instances, the `http` block must include:

```nginx
server_names_hash_bucket_size 128;
server_names_hash_max_size 4096;
include /home/you/.config/rementor/nginx/*.conf;
```

Rementor writes generated routes to that XDG directory and verifies with
`nginx -T` that the generated file is part of the effective configuration
before reloading. It does not edit `/etc/nginx/nginx.conf`, install sudoers
rules, modify `/etc/hosts`, or configure DNS.

Every generated proxy location adds `X-Rementor-*` response proof headers for
the canonical app/service, workspace and environment, effective mode (`local`,
`remote`, or `fallback`), route version, operation ID, and a validated
correlation/request ID. Upstream copies of those headers are hidden before the
Rementor values are added, and the proof headers are exposed to local browser
origins through CORS. Send `X-Correlation-ID` or `X-Request-ID` to preserve a
safe correlation value; otherwise nginx generates one. The
`/__rementor/trace` path returns a small request-inspection payload through the
control plane for debugging stale proxy projections.

The container demo and the user-owned host nginx follow the same privilege
boundary. Do not make a privileged nginx master load files from a directory
writable by untrusted users.

## Security Model

- The unauthenticated control plane binds to loopback only; non-loopback bind
  addresses are rejected.
- The container demo publishes its port to `127.0.0.1` by default. Exposing it
  on another interface is an explicit opt-in for trusted networks.
- Browser mutations require a process-scoped CSRF token.
- Generated routes add CORS headers only for local development origins such as
  `localhost`, `127.0.0.1`, `::1`, and `*.localhost`.
- Workspace, URL, hostname, path, and route-pattern input is validated before
  persistence or nginx rendering.
- Remote URLs containing embedded credentials are rejected.
- The demo container runs as an unprivileged user.

See [SECURITY.md](SECURITY.md) for operational guidance and reporting.

## Development

### Canonical application identities

Each registered application has a stable `appId` and `serviceId`. The legacy
`id` field remains an alias for `appId`, so existing workspace files continue to
load unchanged. A single identity can be bound in multiple environments while
keeping environment-specific route metadata separate:

```text
appId: rtc
serviceId: reforma-tributaria-consumo
aliases: [reforma-tributaria-consumo, front-giss-v2]
workspace: desenvolvimento  -> /rtc
workspace: qualidade        -> /rtc
```

Aliases are normalized (case, whitespace, and separators), and collisions are
rejected instead of selecting an arbitrary application. Resolve or register an
alias with the CLI:

```bash
rementorctl app resolve desenvolvimento front-giss-v2
rementorctl app alias desenvolvimento rtc front-giss-v2
```

### Shared routing contract

The generated protobuf contract is the source of truth for RPC, CLI, MCP, and
the browser. Responses include an explicit `identity` reference, an
`environment` reference, and a typed `route` projection alongside the legacy
`id`, `active`, and timestamp-string fields. Route-affecting mutations return
`operationId`, `routeVersion`, typed creation/completion timestamps, and a
`correlationId`; callers may supply a correlation ID in the request or let
Rementor generate one.

Browser URLs are resolved from the canonical application identity and the
selected workspace/environment public binding. `rementorctl url` and the MCP
`rementor_url` tool return the stable URL separately from the current local or
remote proxy target, together with route version, effective mode, and the last
operation/correlation metadata. Switching an application between local and
remote therefore never changes the URL a browser should open.

Existing workspace files and RPC clients remain compatible: `id` is treated as
the legacy alias for `appId`, `workspaceId` remains the environment key, and
the old boolean/string fields are retained while clients migrate to the typed
messages. RPC failures keep their Connect status and human-readable message,
and carry a `StructuredError` detail with a stable machine-readable code.

### Route metadata and frontend roots

Route registration now separates the browser-facing `publicPath` from the
upstream service `upstreamContext`. `frontendRoot` can be supplied by
registration metadata or a repository manifest when the frontend build base is
known. Paths are canonicalized (including trailing slashes) before routing;
malformed paths and contradictory legacy/new values are rejected before nginx
or SQLite are touched.

For explicit metadata, nginx rewrites the public prefix to the upstream
context while preserving the request suffix. Legacy path/context-only records
retain their historical context-as-ingress behavior until they are explicitly
migrated.

The legacy `path` and `context` fields are read as aliases during migration and
are written alongside the explicit fields so older clients continue to work.
When only legacy metadata is supplied, Rementor preserves its historical
context-as-ingress behavior while exposing a migration warning. A nested public
path without a proven frontend root produces a structured warning with a
stable code, field, severity, and remediation. Pass `--strict-metadata` to
`app register`, `route plan`, or `route apply` (or set the corresponding RPC
flag) to promote selected warnings to validation errors.


Prerequisites:

- Go 1.25
- Node.js 22
- Buf
- `protoc-gen-go`
- `protoc-gen-connect-go`

```bash
make setup
make generate
make build
```

Run the same quality gates used in CI:

```bash
buf lint
make generate
git diff --exit-code
cd web/frontend
npm ci
npm audit
npm run typecheck
npm run build
cd ../..
go vet ./...
go test -race ./...
```

Generated RPC clients are committed. The frontend bundle is generated into
`cmd/server/dist/` and ignored by Git; run `make frontend` before starting the
server or running Go checks.

## Scope

Rementor is a Linux-oriented local development tool, not a production gateway.
It currently compares routing state and local/remote health; request/response
payload diffing is not implemented.

## License

[MIT](LICENSE)

### Following a paired computer

In Configuration, select a paired computer under **Follow a computer**, then
**Follow all environments**. Its environments appear automatically, including
later additions and removals. Extra local environments remain local. Routing
sessions stay separate and retain their pinned baseline.

Toggles, **Local all**, **Remote all**, and **Edit source route** change the source
configuration. Every computer following that source receives the change. The
editor names the source, and no local route override is created. Environment
settings, service add/edit/delete, port registration, and environment deletion
are also available while following and change the source computer. Registration
ports belong to that computer. Concurrent edits are
checked against the source version; conflicts require a refresh and review.
Source outages preserve the validated cached routes but prevent source edits.
Target health is shown separately from configuration sync status.

Turn off an environment’s following switch in Configuration or on its dashboard
to keep its current setup and edit it locally. Other environments continue
following. The choice persists across restarts and source removal or readdition.
Peer endpoints keep their owning computer; opting out does not start those
services locally. Unchanged owner registrations remain usable after source route
selection changes; changed registrations require a new configuration.
Turning the switch back on reads the source’s current authenticated configuration
and replaces local settings after confirmation. Concurrent local edits reject
the operation instead of being overwritten.

**Stop following** restores the environments saved before following and retains
extra local environments and environments you stopped following individually.
Close sessions under source-only environments before
removing those environments or stopping following.

```sh
rementorctl computer-following status
rementorctl computer-following follow --peer <fingerprint> --expected-revision <revision>
rementorctl computer-following sync
rementorctl computer-following exclude --workspace dev --expected-revision <revision> --expected-workspace-version <version>
rementorctl computer-following include --workspace dev --expected-revision <revision> --expected-workspace-version <version>
rementorctl computer-following stop --expected-revision <revision>
```

MCP exposes `rementor.computer_status`, `_follow`, `_sync`, `_exclude`, `_include`, and `_stop`.
`expected_revision` is an unsigned decimal string from status. Legacy
`inheritance` operations remain available for existing per-workspace relationships;
whole-computer environments reject local overrides and per-workspace detach.

Transitive endpoint forwarding currently requires direct pairing with the
endpoint's owning computer. Cycles are rejected when present in the received
lineage; concurrent reciprocal changes on different computers are detected on
subsequent synchronization, without a distributed coordinator. Portable public
origins and shared-ingress replication remain under implementation.

### Public URLs and device selection

Configuration → **Public URLs** maps named HTTPS origins to workspace hosts.
Publish the same origins for each selected environment/session, allow the intended
Tailscale accounts, choose the shared environment, and optionally select a
different environment for individual devices. A device selection still requires
its allowed account. Tagged devices require an explicit selection and never use
the shared default.

The application data plane listens on loopback at the management port plus two
(`9302` by default). `REMENTOR_PUBLIC_INGRESS_PORT` changes that port;
`REMENTOR_PUBLIC_NGINX_ADDRESS` selects an explicit loopback nginx listener when
nginx has multiple listening ports. Port collisions are visible in configuration
and do not prevent existing localhost routing. This listener serves application
traffic and runtime origins, with no management RPC or enrollment routes.

For this computer's Tailscale HTTPS hostname, point Serve at that listener. The
configuration page supplies the command for each configured HTTPS port:

```bash
sudo tailscale serve --bg --https=8444 http://127.0.0.1:9302
```

This uses the computer's existing Tailscale hostname. Moving that hostname to a
different gateway is not yet supported. Saving a URL does not create its DNS or
certificate, and removing the URL revokes application access without deleting
other Tailscale Serve entries.

```bash
rementorctl access show
rementorctl access save --file public-access.json --expected-revision 0
rementorctl access clear --expected-revision 1
```

`public-access.json` is the `configuration.document` object from `access show`.
Versions and account IDs in CLI/MCP JSON are lossless decimal strings. MCP exposes
`rementor.access_show` and `rementor.access_save`. Changes require the current
revision; a successful access-policy edit closes requests and WebSockets using
the previous policy.

Applications can fetch `GET /__rementor/origins` with credentials, or load the
parser-blocking script before their application scripts:

```html
<script src="/__rementor/origins.js"></script>
<script src="/application.js"></script>
```

```javascript
const api = rementor.origin('api');
const response = await fetch(rementor.url('api', '/orders'), {
  credentials: 'include',
});
const events = new WebSocket(rementor.webSocketURL('events', '/socket'));
```

The immutable `rementor.manifest` records the selected environment/session,
selection source, access revision, route version, and separate HTTP/WebSocket
origin maps. Missing origin IDs and paths that change origin are rejected.
`routingHosts` maps each origin ID to the selected routing hostname for
applications that use hostname labels as domain metadata. Requests still use the
public origins, and neither manifest exposes an upstream loopback endpoint.
The [legacy GISS build adapter](integrations/giss/README.md) loads this contract
before Angular starts, selects `URL_BASE`, and preserves municipality lookup and
localhost builds.

### Automatic Tailscale sharing

Configuration → **Share over Tailscale** previews recognizable service names
from the selected environment/session's local hosts, such as
`https://feature-x-dev-giss.<tailnet>.ts.net`. Save the proposed origins and
allowed accounts, then Rementor enables sharing. Subsequent off/on changes need
no terminal command. Existing origin IDs, separate application hosts, and
device selections are preserved when converting manually configured URLs.
Local `.localhost` routes continue to work.

The toggle persists desired publication state. The daemon retries every 15
seconds and after toggle/access-policy changes, including after restart. It
creates internal Services automatically when an API key is supplied, installs
HTTPS on port 443, and advertises the gateway. Without an API key, create the
suggested internal `svc:<name>` Services with endpoint `tcp:443` once in the
[Tailscale admin console](https://console.tailscale.com/admin/services).
Host approval or an appropriate auto-approval policy is still required.

One-time gateway setup:

- Use a **dedicated tagged Tailscale gateway** with MagicDNS and HTTPS enabled.
  Rementor does not retag or reenroll your everyday computer.
- Set `REMENTOR_TAILSCALE_GATEWAY_SOCKET` to that gateway's tailscaled socket.
  If omitted, the publisher uses `REMENTOR_TAILSCALE_SOCKET`, then the system
  socket. Give the Rementor user operator access to that gateway.
- Optionally set `REMENTOR_TAILSCALE_API_TOKEN` in the daemon environment to a
  Tailscale API key authorized to read/create Services. It is used only by the
  daemon with HTTP Basic authentication, never saved in SQLite or sent to the UI,
  CLI/MCP, logs, application origins, or subprocess arguments. An OAuth bearer
  token is not interchangeable with this API key.
- `REMENTOR_TAILSCALE_SOCKET` remains the user-device catalog socket when using
  a separate gateway, so initial sharing allows the user's existing account.
  The gateway socket resolves incoming device identities.

Each distinct application origin receives a Service; aliases using the same
origin share its registration. Route/backend toggles remain entirely inside
Rementor. Serve writes use ETags, preserve unrelated configuration, and refuse
to adopt existing local service entries. Disabling removes only this gateway's
owned Serve entries and advertisements. Failed removals remain durable and
visible for retry; service definitions remain reserved in the tailnet, allowing
stable names to be reused by other enrolled gateways.
Ownership also records the gateway's Tailscale node identity. Changing the
socket/node while entries remain owned requires restoring the original gateway
to reconcile them; Rementor does not clean up another node's configuration.

An enrolled shared gateway publishes the shared owner's policy and follows URL
changes automatically. The original publisher can promote its own service
definition into the shared group, preserving allocated addresses and other
metadata. Moving shared ownership retains the group's service identity and
routing computer. Disabling one gateway does not disable other gateways.

The UI distinguishes setup requirements, failed registration/removal, pending
verification and ready URLs. Ready requires an authenticated HTTPS origin
manifest matching the current policy and an application response carrying
Rementor's route proof. It is not a claim that application login, browser cookies
or a physical gateway handoff has been tested. Live named-Service movement still
requires the dedicated tagged-gateway acceptance described in
[the provider research](docs/research/tailscale-routing.md).

```sh
rementorctl tailscale-sharing show
rementorctl tailscale-sharing prepare --workspace <environment-or-session>
# Save the proposed document with access save (or shared-ingress save for a group).
rementorctl tailscale-sharing on --expected-revision <sharing-revision>
rementorctl tailscale-sharing off --expected-revision <sharing-revision>
```

MCP exposes `rementor.tailscale_sharing_show`, `_prepare`, and `_set`. Preparation
is read-only. Mutations require the current sharing revision as a decimal string.

### Shared ingress ownership

Configuration → Shared URLs lets a computer share its published URLs, enroll
paired gateways, or join an ingress whose owner has enrolled it. The owner edits
the shared origin/device policy. Transferring ownership moves that control while
preserving the routing computer and selected application endpoints. Resume or
cancel a pending transfer from the same section; committed transfers cannot be
rolled back by cancellation.

The CLI and MCP use the same versioned local RPC:

```sh
rementorctl shared-ingress show
rementorctl shared-ingress workspaces --routing-computer <fingerprint>
rementorctl shared-ingress create --file shared.json --expected-revision 0
rementorctl shared-ingress join --owner <fingerprint> --expected-revision 0
rementorctl shared-ingress save --file shared.json --expected-version <version>
rementorctl shared-ingress transfer --destination <fingerprint> --expected-version <version>
rementorctl shared-ingress recover --expected-revision <revision>
rementorctl shared-ingress cancel --expected-revision <revision>
rementorctl shared-ingress sync
```

`shared.json` contains `policy` (the public-access document),
`routingOwnerFingerprint`, and `gateways` (paired fingerprints including the
selector owner). Its bindings refer to workspaces on the routing computer.
Read the current revision/version from `show` before editing. MCP exposes
`rementor.shared_ingress_show/workspaces/create/save/join/transfer/recover/cancel/sync`;
revision/version/account IDs use decimal strings.

Shared forwarding uses the pinned peer listener and fails when its current owner
is unavailable. Stable DNS/HTTPS ingress movement remains subject to the provider
proof; these controls do not provision a Tailscale Service or certificates.
