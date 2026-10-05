# Service toggle latency

Implemented and installed locally on 2026-09-08. Target: browser-visible toggle p80 ≤ 200 ms in the existing large environment.

## Baseline and cause

The original desenvolvimento job toggle took about 2,250 ms in the mutation RPC; the following GetWorkspace request took 2–3 ms. A click caused five nginx commands (`-t`, `-t`, `-T`, `-s reload`, `-T`) against a 14.3 MB configuration with 4,829 locations and 63 server blocks. Rendering rebuilt and sorted the workspace route projection for every application, and repeated conflict comparisons normalized their inputs for every pair.

## Changes

- Share response headers at server scope; retain service-specific proof through URI/proof maps. Coalesce non-overlapping routes with identical proxy behavior. Leave rewrites, redirects and overlapping routes explicit. Preserve upstream URI, Host, TLS and error-response proof behavior.
- Compute normalized routes once per workspace during rendering, and normalize conflict matcher inputs once before pair comparisons.
- Cache rendered workspace fragments by their routing inputs, including legacy path semantics, operation metadata and listener settings. Health/projection timestamps do not invalidate the cache. The live generated file is about 1.28 MB with 513 locations and the same 63 server blocks.
- Give nginx one apply-and-verify boundary. Cold applies inspect the included configuration; warm applies signal the verified master directly while the inspected base files and previously generated file remain unchanged. The master validates the candidate during reload. A fresh connection must receive the generated configuration fingerprint from a new worker before success.
- On Linux, gracefully retire the captured old worker generation after new-worker proof. Verify parent PID and process start time, and wait for old listening sockets to close. In-flight requests drain normally. This avoids nginx's fixed 100 ms pause before its own graceful retirement signal; other platforms retain the normal wait. This follows nginx's [master/worker reload sequence](https://github.com/nginx/nginx/blob/master/src/os/unix/ngx_process_cycle.c).
- Reuse a workspace's effective-route snapshot when projecting its applications. Save only the changed workspace when the persistence adapter supports it; older adapters retain full-snapshot saves. Cache database migration checks by database file identity and SQLite schema version.
- Add accessible busy state and pending animation to card/table switches. Completion remains tied to confirmed routing and persistence, not optimistic UI feedback.

## Measurement

Two independent 40-toggle browser runs passed (p80 149 ms and 147 ms). The second run measured click → confirmed state and enabled switch, including RPC, workspace refresh and DOM update:

| Metric | Milliseconds |
| --- | ---: |
| p50 | 137 |
| p80 | 147 |
| p95 | 153 |
| Maximum | 164 |

A final 20-toggle check after the final service rollout also passed: p50 **143 ms**, p80 **174 ms**, p95 **213 ms**, maximum **234 ms**. The target is p80, not a hard maximum; this last run includes a few slower samples. The job route was restored to local after all measurements.

Samples alternate local/remote for the job service, with 250 ms between completed toggles, on the full current configuration. No configurations were removed to achieve the result. These are warm service measurements; startup, external configuration changes, DNS stalls and failed applies are not covered by the 200 ms target.

Reproduce in the Rementor browser console by loading `scripts/measure-toggle.js` and running:

```js
await measureToggle('job', 40, 250)
```

The helper restores the starting toggle state, returns all samples and asserts the target through its `pass` result. Nearest-rank percentiles are used. The generated proof endpoint also exposes its worker PID so reapplying identical configuration cannot mistake the old worker for a new generation.

## Validation

- Full `go test -race ./...`, Go vet, protobuf lint, frontend typecheck and production build passed.
- Real-nginx integration tests cover proof across local/remote/error responses, compact exact/prefix routes and unmatched paths, repeated identical applies, continued in-flight requests, and restoration after a candidate rejected by nginx.
- Cache tests cover unchanged output, toggles, fresh/cached equivalence, legacy-path changes, database replacement and schema changes.
- Existing registry tests cover routing/persistence failure and rollback, route versions, conflicts and idempotency.
