# Inspect fleet routing

This experiment evaluates routing intent locally. The `.example.test` URLs are
illustrative and do not resolve; the command does not start a gateway, contact a
node, configure Tailscale, or modify existing workspaces.

Brutus owns the environment used by shared ingress. Marcus follows that
environment and overrides billing with a process on Marcus. Phones consume the
shared ingress environment without running Rementor.

```bash
# A phone's shared ingress: orders runs on Brutus.
go run ./cmd/rementorctl topology resolve --file examples/fleet/topology.json \
  --url https://services.rementor.example.test/orders/42

# Marcus inherits the same orders endpoint, still owned by Brutus.
go run ./cmd/rementorctl --json topology resolve --file examples/fleet/topology.json \
  --instance marcus-dev --url https://services.rementor.example.test/orders/42

# Marcus's override sends billing to Marcus instead.
go run ./cmd/rementorctl topology resolve --file examples/fleet/topology.json \
  --instance marcus-dev --url https://services.rementor.example.test/billing

# Explicitly targeting Brutus fails visibly when its endpoint is unavailable.
# Exit status is nonzero; JSON still includes the failed selection.
go run ./cmd/rementorctl --json topology resolve --file examples/fleet/topology.json \
  --instance marcus-dev --url https://services.rementor.example.test/orders \
  --unavailable-nodes brutus
```

Change Brutus's `orders` override to `orders-remote` and rerun either orders
command. Both instances now choose the remote target; Marcus's billing override
is unaffected. The inspector reads one detached snapshot on each invocation.
Automatic live synchronization between running instances is a later milestone.

Selections use the nearest instance override, then its inherited environment,
then the route's baseline target. Exact paths win over prefixes; longer prefixes
win over shorter ones and match on path-segment boundaries. Origins distinguish
scheme and port. WebSocket URLs use the corresponding HTTP(S) origin.

Target kinds are `remote`, `node` (with explicit owner), and `route`. A `route`
target follows the referenced route's effective selection in the serving
instance's environment; it does not perform an HTTP redirect or a second path
match. Alias and environment-inheritance cycles are rejected. Path rewrites,
upstream headers, CORS, TLS, and health probes are not evaluated by this slice.

Unknown instances fail instead of silently selecting the shared environment.
An omitted instance selects `sharedInstance`. Unavailable endpoints fail without
automatic fallback. Targets not explicitly marked unavailable have unknown
health; `selected` means intent was resolved, not that a live request succeeded.

See the [implementation plan](../../docs/plans/tailscale-shared-workspaces.md).
