# Worktree routing sessions

Status: first implementation complete and validated with an isolated local
preview. The installed general-development configuration has not been changed.

Implemented: persistent session projections and baseline snapshots; generated
session and per-application hostnames; explicit sparse port registration;
existing toggle/health semantics; metadata editing and session-only applications;
preview/apply baseline refresh with concurrency tokens; session selectors in
RPC/CLI/MCP; UI lifecycle controls and environment/session labels; shared-endpoint
visibility; routing proof and browser URLs with parent environment identity.

Validation: service tests exercise concurrent registrations, isolation, sharing,
stale refresh/editor rejection, refresh conflicts, hostname ownership, and
routing/persistence rollback. SQLite tests cover restart persistence and snapshot
identity handling. An RPC/CLI/MCP integration test drives real nginx with two
local instances and a remote baseline. The browser flow has been exercised using
neutral local mock services.

Worktree paths remain external descriptive context in this first version; no
worktree-local association file is created. Use explicit session IDs or the
per-process REMENTOR_SESSION setting. Real application authentication/callback
configuration and absolute API URL settings still require application-specific
verification.

## Outcome

Multiple coding agents can work against the same remote environment through
one Rementor daemon and nginx instance. Each feature has its own browser origin,
local endpoint registrations and route choices. Sessions may explicitly share
a local process. The general development
workspace and other features retain their routing settings.

Example (hostnames and ports are illustrative):

| Scope | Browser origin | `/orders` | `/billing` | Other paths |
| --- | --- | --- | --- | --- |
| General development | `desenvolvimento.giss.localhost` | Existing setting | Existing setting | Existing setting |
| Feature X | `feature-x.desenvolvimento.giss.localhost` | Local port 24001 | Remote dev | Remote dev |
| Feature Y | `feature-y.desenvolvimento.giss.localhost` | Local port 24002 | Local port 24003 | Remote dev |

Both features can modify the same application concurrently. The router selects
the session by hostname and then selects the application by the existing route
rules. No feature prefix is added to application paths.

## Existing implementation

- `internal/models/models.go`: `WorkspaceID` currently also identifies the
  environment; application bindings contain `Port`, `Active`, and route state.
- `internal/nginx/config.go`: one renderer already produces multiple hostname
  server blocks on shared listeners. Route validation currently invokes
  workspace-scoped conflict checks.
- `internal/services/browser_url.go`: browser URL resolution takes workspace
  and application identity, without a separate session selector.
- Registry/provider/store code already coordinates routing application,
  persistence, operation metadata, rollback, and recovery.
- `docs/plans/toggle-latency.md` records ongoing work to reduce global reload
  cost. Session rendering must preserve those optimizations.

Git worktrees isolate source files; they do not isolate listening ports or
Rementor state. Cloning a workspace with its current ports and active toggles
would carry those collisions into every feature.

## Proposed model

Introduce a **routing session** belonging to an existing environment workspace.
Keep the existing workspace/environment contract intact for legacy callers.

Persist these concepts separately:

- Session: immutable ID, display name, environment workspace ID, unique public
  hostname bindings, baseline snapshot revision, session revision, lifecycle
  state, and timestamps.
- Session baseline: snapshot of remote routing metadata, canonical application
  identities, public paths, upstream contexts, frontend roots, and remote
  targets. Exclude the base workspace's local ports and active local modes.
- Session application binding: app ID, explicit local port, optional worktree
  path, session-specific routing/health overrides, desired mode, and operation
  state. It does not depend on a particular process manager or port tool.

A session can include several repositories/worktrees for a cross-service
feature. A branch name is a label, never the unique session key. The same
worktree can have sessions for different environments. A worktree path is
optional descriptive metadata; routing uses the registered endpoint.

Start with a pinned baseline. Ordinary development toggles and later environment
edits do not silently change a running feature. An explicit refresh previews
remote metadata changes and preserves session overrides; removed or conflicting
applications require resolution before applying. Local-only applications have
no remote baseline and remain unavailable until explicitly bound.

Unmodified routes use the remote development stack. They must never proxy through
the general workspace's public local URL, because that would inherit its toggles.
Registration leaves a new binding in remote mode. Toggling local succeeds even
when the target is unhealthy or absent, matching existing behavior. Local and
remote health remain visible; requests to an unavailable local target receive
the applicable nginx/upstream error. Do not add a readiness gate or health-driven
fallback.

Sessions may override inherited application configuration, including local port,
upstream context, and health endpoint, and register new applications without
changing the base workspace. Existing registration validation applies. A new
application without a remote target is visibly unavailable in remote mode.

## Hostnames and request propagation

Generate stable hostnames by prefixing each inherited environment or
application hostname with the readable session label. For example,
`feature-x.desenvolvimento.giss.localhost` and
`feature-x.dev.giss.localhost`. Session IDs remain internal uniqueness keys;
the prefix is unique within its parent environment.
Validate ownership globally across sessions, workspace domains, application
domains, and the control-plane hostname, including wildcard overlaps. Reuse
existing route precedence rules within each session.

Generate session equivalents for per-application hostnames where needed. URL
resolution must accept a session and return its public origin, effective target,
environment, and session revision. Extend proof headers and trace responses
with a session ID while retaining the actual environment identity.

All browser requests must keep the session origin. Relative API URLs are the
preferred application integration; otherwise tooling must provide the session
API origin, websocket/HMR origin, and callback URLs. A frontend hardcoded to
`api.localhost` would escape isolation even when its HTML came from a session.
Do not assume arbitrary HTML/JavaScript response rewriting can fix this.

Verify local DNS resolution and forwarding of generated hostnames through the
installed outer proxy as part of an end-to-end spike. The existing Tailscale
sharing plan is separate; do not make remote sharing a prerequisite.

## Ports and local processes

The user or agent obtains a local port from their own setup and supplies it when
registering an application in a session. Rementor records that value; it does not
allocate, reserve, launch, or wait for a process. A service without a base local
port can receive a session registration in the same way. Independent processes
need distinct listening endpoints, managed outside Rementor.

Two sessions may deliberately register the same local endpoint to use one shared
process. Show that the endpoint is shared rather than rejecting the registration.
Changing one session's toggle or registration does not change the other's route,
although stopping their shared process naturally affects both.

Process startup/shutdown, debug ports, worktree creation/deletion, and automatic
process-manager integration are outside scope. Closing a session removes only
its routes and session configuration. It does not stop applications or edit
external tooling settings.
Sessions and desired modes survive daemon/computer restarts until explicitly
closed; resume normal health reporting without a readiness gate or automatic
expiry. An endpoint registration identifies an address and port, not a verified
process identity; its owner must update registrations when repurposing ports.

## Agent workflow

Implemented command shapes:

```bash
rementorctl session create feature-x --workspace desenvolvimento --json
# Register the port selected by your local setup; no process is started.
rementorctl app register desenvolvimento orders-api --session <session-id> --port 24001
# Registration inherits route metadata and leaves the new binding remote.
# Later, select local independently of process health:
rementorctl route apply desenvolvimento orders-api --session <session-id> --mode local
rementorctl url --workspace desenvolvimento --app orders-api --session <session-id>
rementorctl session inspect <session-id> --json
rementorctl session close <session-id>
```

Explicit `--session` selects the routing context. Use a per-process
`REMENTOR_SESSION` default; a worktree-local association is a future convenience.
Conflicting explicit environment/session selectors fail. Never introduce
a machine-global current session. A missing or stale session association must
produce an error rather than silently mutate general development.

CLI, MCP, RPC, and UI expose the same operations. The UI groups sessions beneath
their environment and reuses registration, toggle, and local/remote health controls.
Keep both environment and session visible. Browser selection is local to that UI
context and cannot change another agent's CLI/MCP context. UI, CLI, and MCP all
return the selected session's browser URL.

## Transaction and module design

Keep session creation, registration, refresh, inspection, and close behind one session
module interface. Hide hostname allocation, endpoint registration, baseline composition,
and lifecycle checks there. Resolve the baseline plus overrides into detached
routing projections for the existing routing transaction and renderer.

Do not represent session IDs as environment IDs in public responses or proof.
Adapt the internal rendering input if necessary while reusing route normalization,
path rewriting, validation, and compact rendering.

Each session has its own optimistic revision and scoped idempotency keys. Since
nginx remains shared, serialize global apply/persist/publish through the daemon.
Build the full candidate from the latest committed state while holding the
mutation coordination mechanism; otherwise an agent's stale snapshot can erase
another session. Rollback and recovery must retain unrelated committed sessions.
Distinguish per-session revisions from the global proxy generation/fingerprint.

## Delivery sequence and acceptance

1. Prove routing with two session hostnames and two instances of the same mock
   application, plus a remote-only dependency, through one real nginx instance.
   Exercise API calls, websockets, redirects, and browser URL resolution. Identify
   any real frontend/auth integration requirements before committing to rollout.
2. Add session/baseline persistence and projection, global hostname validation,
   session route metadata, and migrations preserving existing workspace behavior.
3. Add explicit session port registration, configuration overrides, new
   applications, and shared-endpoint visibility. Reuse existing toggle and health
   behavior, including services with no base local port. Expose through protobuf,
   CLI, and MCP.
4. Extend plan/apply/resolve/url/trace and UI with session context, revision checks,
   and explicit refresh/close. Regenerate clients and document agent usage.
5. Validate concurrent mutations, failure rollback, restart recovery, and practical
   session-count rendering/reload cost against the existing latency baseline.

Acceptance tests must demonstrate:

- X and Y route the same app to different processes; general development stays
  unchanged, and untouched session routes remain remote despite base toggles.
- Concurrent create/register/apply operations preserve both sessions, reject
  conflicting public hostname ownership, and scope stale-version/idempotency
  handling correctly. Explicit shared local endpoints remain allowed.
- Failed nginx application or persistence restores the previous global state.
- Closing X leaves Y and general development working; restarting the daemon
  preserves session identity, registrations, and desired modes while health
  reporting resumes independently.
- An authenticated browser flow retains its session across frontend/API requests,
  HMR/websockets, and required redirects; hardcoded-origin escapes are surfaced.
- Registration starts no process and leaves new bindings remote. Toggling an
  unhealthy local target succeeds and preserves the selected mode.
- Session-only applications and metadata overrides leave the base unchanged;
  explicit baseline refresh previews changes and preserves overrides.
- Shared-endpoint registrations are visible and permit independent toggles.
- Existing CLI/RPC clients and workspace routes retain their current behavior.

## Scope and decisions to validate

This isolates routing configuration, not local process ownership. Remote databases, queues,
authentication systems, and other remote side effects remain shared. Calls made
inside the remote stack do not automatically return through a feature session;
local backends needing session-aware downstream calls require explicit endpoint
configuration too.

Validate hostname forwarding and frontend API/auth callback/allowed-origin
compatibility during the spike. Agreed first scope:
same-machine path-based environments with existing worktrees, pinned baselines,
explicit cleanup, and no automatic Git/process/container management.
