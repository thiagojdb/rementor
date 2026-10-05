// Copy this file into frontend-build-scripts/cmd/front-giss-v2-dev.
package main

import (
	"encoding/json"
	"os"
	"strings"
)

func runtimeOriginID() string { return strings.TrimSpace(os.Getenv("REMENTOR_API_ORIGIN")) }

func runtimeBaseExpression(fallback string) string {
	encoded, _ := json.Marshal(fallback)
	if runtimeOriginID() == "" {
		return string(encoded)
	}
	return "window.gissRuntime.apiBase(" + string(encoded) + ")"
}

// The external script blocks parsing, so the manifest is ready before Angular's
// constants and automatic bootstrap. Local builds need no ingress endpoint.
func injectRuntimeOrigins(markup string) string {
	api := runtimeOriginID()
	if api == "" || strings.Contains(markup, `id="giss-runtime-origins"`) {
		return markup
	}
	id, _ := json.Marshal(api)
	bootstrap := `<script id="giss-runtime-origins">(function () {
 'use strict';
 var api = ` + string(id) + `;
 var host = window.location.hostname;
 var local = host === 'localhost' || /\.localhost$/.test(host) || host === '127.0.0.1' || host === '[::1]';
 window.gissRuntime = Object.freeze({
  apiBase: function (fallback) {
   if (local) return fallback;
   if (!window.rementor) throw new Error('Rementor runtime origins are unavailable');
   return window.rementor.origin(api);
  },
  subdomain: function () {
   var routingHost = host;
   if (!local) {
    if (!window.rementor || !window.rementor.manifest.routingHosts || !window.rementor.manifest.routingHosts[api]) throw new Error('GISS routing hostname is unavailable');
    routingHost = window.rementor.manifest.routingHosts[api];
   }
   return routingHost.split('.giss.')[0].split('.').pop();
  }
 });
 if (!local) document.write('<script src="/__rementor/origins.js"><\/script>');
})();</script>
`
	if index := strings.Index(strings.ToLower(markup), "<head>"); index >= 0 {
		index += len("<head>")
		return markup[:index] + "\n" + bootstrap + markup[index:]
	}
	return bootstrap + markup
}
