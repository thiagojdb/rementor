# Tailscale Shared Workspaces

## Problem

Rementor currently gives each developer a stable local URL, usually a
`*.localhost` hostname rendered into nginx. That works on the host machine, but
it does not naturally share across other devices:

- `*.localhost` is always local to the device making the request.
- A second Linux client, phone, or tablet cannot resolve the host machine's
  local Rementor routes by using the same `*.localhost` hostname.
- Some frontends are built with an absolute API base URL such as
  `http://api.localhost`. That URL points to the wrong machine when the same
  build is used remotely.
- Some APIs depend on nonstandard request headers; shared proxies must preserve
  them.

An early proof of concept used an SSH tunnel and nginx on a second machine to
proxy local development hosts back to the Rementor instance on the host. It
worked after enabling the required request-header handling, but it is not a
productized Rementor workflow and does not help phones.

## Goals

- Add a first-class shared workspace mode that exposes selected Rementor
  workspace origins over Tailscale.
- Let the same frontend build work on the host, another Linux client, and
  phones by using the request origin as the backend base URL.
- Keep changes out of upstream application repositories; custom behavior belongs
  in Rementor or its development-tooling integration.
- Preserve Rementor's local workflow. Existing `*.localhost` routes should
  continue to work.
- Generate proxy config that preserves required application request headers.
- Prefer HTTPS for shared origins so browser APIs and existing websocket
  conversions work consistently.

## Non-Goals

- Do not expose Rementor to the public internet.
- Do not make the unauthenticated control plane remotely reachable.
- Do not require commits to upstream applications.
- Do not change production frontend behavior.
- Do not require every workspace to be shared.

## Proposed Architecture

Each workspace can have two classes of origins:

- Local origin: current behavior, for example `http://api.localhost`.
- Shared origin: tailnet-reachable behavior, for example
  `https://api.<node>.<tailnet>.ts.net` or another configured tailnet DNS name.

The shared origin should be a same-origin proxy:

- `/` routes to the active frontend route.
- `/service-*` and other registered paths route through the same Rementor
  routing table used locally.
- Websocket upgrade headers are preserved.
- Request headers are passed through, including headers with underscores.
- The Rementor UI/control plane remains loopback-only unless a separate
  authenticated remote-control design is added later.

The nginx renderer should generate extra `server` blocks for shared origins
instead of requiring a second manually managed proxy. Shared `server` blocks can
reuse the existing routing location generation, but they need different listen
and CORS behavior.

## Data Model

Add shared-origin configuration to the workspace routing model:

```text
Workspace
  Routing
    local_domain
    default_remote_base_url
    shared_origins[]
      name
      host
      scheme
      listen_host
      listen_port
      provider
      enabled
```

Initial provider value should be `tailscale`. Keep the model extensible enough
for future providers such as direct LAN, SSH tunnel, or custom reverse proxy.

Implementation surfaces:

- `internal/models/models.go`
- `proto/rementor/v1/rementor.proto`
- `internal/config/config.go` SQLite migrations and load/save mapping
- `internal/rpc/service.go` model conversion
- `web/frontend/src/api/types.ts` and generated protobuf TypeScript

## CLI Workflow

Add explicit share commands:

```bash
rementorctl share enable demo \
  --host api.<node>.<tailnet>.ts.net \
  --provider tailscale \
  --scheme https

rementorctl share status demo
rementorctl share disable demo
```

The command should:

- Validate the shared hostname.
- Persist the shared-origin config.
- Re-render and reload nginx through the existing routing provider.
- Print the local and shared URLs for the workspace.

Implementation surfaces:

- `internal/cli/*.go`
- `internal/services/registry.go`
- `internal/services/routing_provider.go`

## nginx Rendering

Extend `internal/nginx/config.go` so `buildConfig` emits shared `server` blocks
for enabled shared origins.

Required nginx behavior:

```nginx
underscores_in_headers on;
ignore_invalid_headers off;
proxy_pass_request_headers on;
proxy_http_version 1.1;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection $connection_upgrade;
```

Shared server behavior:

- Use the shared hostname as `server_name`.
- Listen on the configured Tailscale-facing address or an explicitly configured
  local address intended for `tailscale serve`.
- Reuse the same route selection logic as the local workspace server.
- Preserve the original request `Host` when proxying to local frontend routes
  where the frontend needs `window.location.origin`.
- Preserve the existing remote upstream `Host` behavior for remote services
  that expect their configured host.
- Add CORS allowances for the configured shared origins, not only localhost.

Add renderer tests that assert:

- Shared origins produce additional server blocks.
- Required custom request headers are not stripped.
- Websocket headers are present.
- Existing local-only configs are unchanged when no shared origins are enabled.

## Tailscale Integration

Support two operating modes:

1. Direct nginx binding:
   - nginx listens on the host's Tailscale IP or all interfaces.
   - Tailnet ACLs restrict access.
   - Rementor validates this is an explicit opt-in.

2. Tailscale Serve:
   - Rementor renders a loopback HTTPS target.
   - A CLI helper prints or optionally applies the required `tailscale serve`
     command.
   - This gives phones a trusted HTTPS origin inside the tailnet.

Start with generated instructions and status checks before making Rementor run
`tailscale serve` directly. Automatic `tailscale serve` management can be added
after the config model is stable.

Status checks should report:

- Tailscale CLI availability.
- Current node name.
- Tailnet DNS name if available.
- Whether the shared host resolves locally.
- Whether the shared URL returns a response.

## Frontend Build Integration

Do not modify upstream applications. The development-tooling layer should
provide a Rementor-specific URL base mode.

Current custom behavior emits an absolute API base URL similar to:

```text
http://api.localhost
```

Add a shared-build mode that uses:

```js
window.location.origin
```

Recommended interface:

```bash
REMENTOR_URL_BASE_MODE=origin
```

or:

```bash
--url-base-mode=origin
```

Default behavior remains the current absolute `*.localhost` URL.

Why this solves the shared-build problem:

- On the host, `window.location.origin` is `http://api.localhost`.
- On another Linux client, it can be
  `https://api.<node>.<tailnet>.ts.net`.
- On a phone, it is the same HTTPS tailnet origin.
- API calls such as `/service-orders/api/items` stay same-origin.

Existing frontend websocket conversion may derive a WebSocket URL from the API
base, for example with `URL_BASE.replace(
'https://', 'wss://')`, so shared phone/client usage should prefer HTTPS.

Implementation belongs in development tooling, not in the application repo.

## Implementation Phases

1. Shared-origin model and persistence
   - Add protobuf fields and regenerate Go/TypeScript clients.
   - Add SQLite migration and load/save mapping.
   - Surface shared origins in workspace RPC responses.

2. nginx shared server rendering
   - Add renderer support for shared origins.
   - Add underscore-header, websocket, and shared-CORS support.
   - Keep existing local-only render output stable.

3. CLI and registry operations
   - Add `share enable`, `share status`, and `share disable`.
   - Validate hostnames and apply routing through the existing transaction path.
   - Print actionable Tailscale Serve instructions when needed.

4. Tailscale status helpers
   - Detect Tailscale availability and node identity.
   - Report whether the shared URL is reachable.
   - Keep automatic Tailscale mutation optional.

5. Custom frontend build mode
   - Add `REMENTOR_URL_BASE_MODE=origin` support in the custom frontend build
     integration.
   - Verify generated frontend configuration can use a runtime JavaScript expression.
   - Keep normal environment builds unchanged.

6. End-to-end validation
   - Test local host access through `http://api.localhost`.
   - Test Linux client access through the shared Tailscale origin.
   - Test an authenticated API request with required custom headers.
   - Test a websocket route from the HTTPS shared origin.
   - Test a phone browser through the tailnet URL.

## Acceptance Criteria

- A workspace can be shared with one CLI command and disabled with one CLI
  command.
- Local `*.localhost` behavior remains unchanged.
- A shared HTTPS tailnet URL serves the same frontend and routes API calls
  through Rementor.
- The same frontend build works from the host, another Linux client, and a
  phone.
- Requests that require custom headers succeed through the shared proxy.
- Generated nginx config is validated before reload and rolled back on failure.
- Rementor's control plane stays loopback-only by default.

## Open Decisions

- Whether the first implementation should prefer Tailscale Serve or direct
  nginx binding.
- Final shared hostname convention.
- Whether shared origins are configured per workspace only, or also per
  application domain.
- Whether Rementor should eventually manage `tailscale serve` state itself.
- Whether future frontends should use a runtime config endpoint instead of
  compile-time constants.
