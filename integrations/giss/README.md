# Legacy GISS runtime origins

The local Go builder can generate one frontend artifact that uses Rementor's
selected public API origin and routing hostname. No application source changes
are required. Existing builds retain their `BASE_URL` / `GISS_URL` behavior.

Install into the local helper:

```sh
python3 integrations/giss/install.py /home/thiago/eicon/giss/frontend-build-scripts
```

Set `REMENTOR_API_ORIGIN=api` in the selected devctl session's frontend binding,
alongside its existing `BASE_URL`. Use the configured devctl build/watch/server
lifecycle. The value names an origin configured in Rementor; use another ID if
the API origin has a different name.

For public access, map that origin to the selected environment/session's GISS
routing hostname, such as `dev.giss.localhost` or `feature.qua.giss.localhost`.
The builder loads `/__rementor/origins.js` before Angular starts. `URL_BASE` uses
the named public origin. Municipality lookup uses the corresponding
`manifest.routingHosts` entry, including the correct environment label for a
session. The physical ingress computer's hostname is not used for that lookup.

On localhost and loopback URLs the same artifact uses its compiled `BASE_URL`
and existing hostname lookup without fetching a runtime manifest. On public URLs,
a missing manifest or API origin stops initialization; it never silently sends
requests to the compiled localhost fallback. Missing routing-host metadata rejects
the municipality lookup.

The adapter's tests execute the generated JavaScript and cover script ordering,
public environment/session selection, missing runtime metadata, and localhost
compatibility. Run the installed helper's `go test ./...` too. Real GISS browser
login and cross-device access remain separate acceptance checks.
