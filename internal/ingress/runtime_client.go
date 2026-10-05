package ingress

import (
	"encoding/json"
	"io"

	"github.com/thiagojdb/rementor/internal/access"
)

// A parser-blocking script before an application's module scripts supplies the
// same build with ingress-selected origins. JSON remains available to async
// consumers at ManifestPath. Neither contract contains upstream addresses.
func writeRuntime(w io.Writer, manifest access.Manifest) {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return
	}
	_, _ = io.WriteString(w, `(() => { "use strict"; const manifest = `+string(raw)+`;
Object.freeze(manifest.origins); Object.freeze(manifest.routingHosts); Object.freeze(manifest.webSocketOrigins); Object.freeze(manifest);
function origin(id) { const value = manifest.origins[id]; if (!value) throw new Error("Unknown Rementor origin: " + id); return value; }
function resolve(id, path, socket) {
 if (typeof path !== "string" || !path.startsWith("/") || path.startsWith("//") || /[\\\u0000-\u0020]/.test(path)) throw new Error("Expected an absolute application path");
 const base = socket ? manifest.webSocketOrigins[id] : origin(id); if (!base) throw new Error("Unknown Rementor origin: " + id);
 const result = new URL(path, base); if (result.origin !== base) throw new Error("Application path changed origin"); return result.href;
}
Object.defineProperty(globalThis, "rementor", { configurable: true, value: Object.freeze({ manifest, origin, url: (id, path) => resolve(id, path, false), webSocketURL: (id, path) => resolve(id, path, true) }) });
})();`)
}
