package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestPortableGISSBootstrap(t *testing.T) {
	t.Setenv("REMENTOR_API_ORIGIN", "api")
	markup := injectRuntimeOrigins(`<html><head><script src="js/app.js"></script></head></html>`)
	if strings.Index(markup, "giss-runtime-origins") > strings.Index(markup, "js/app.js") || injectRuntimeOrigins(markup) != markup {
		t.Fatal("runtime must precede application scripts and inject only once")
	}
	for _, tc := range []struct {
		name, host, api, routingHost, wantBase, wantSubdomain string
		wantLoad, wantError                                   bool
	}{
		{"public dev", "gateway.example.test", "https://api.example.test", "dev.giss.localhost", "https://api.example.test", "dev", true, false},
		{"public session", "gateway.example.test", "https://api.example.test", "feature.qua.giss.localhost", "https://api.example.test", "qua", true, false},
		{"local session", "feature.dev.giss.localhost", "", "", "http://local.giss.localhost", "dev", false, false},
		{"loopback", "127.0.0.1", "", "", "http://local.giss.localhost", "1", false, false},
		{"missing manifest", "gateway.example.test", "", "", "", "", true, true},
		{"missing GISS host", "gateway.example.test", "https://api.example.test", "", "", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]any{"markup": markup, "expression": runtimeBaseExpression("http://local.giss.localhost"), "host": tc.host, "api": tc.api, "routingHost": tc.routingHost})
			cmd := exec.Command("node", "-e", `const vm=require('node:vm'); const p=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
let loads=0;
const context={window:{location:{hostname:p.host}},document:{write(s){if(s!=='<script src="/__rementor/origins.js"></script>') throw Error('wrong runtime URL');loads++; if(p.api) context.window.rementor={origin(id){if(id!=='api')throw Error('unknown origin');return p.api},manifest:{routingHosts:p.routingHost?{api:p.routingHost}:{}}};}}};
vm.createContext(context);vm.runInContext(p.markup.match(/<script id="giss-runtime-origins">([\s\S]*?)<\/script>/)[1],context);
let result={loads}; try{result.base=vm.runInContext(p.expression,context);result.subdomain=context.window.gissRuntime.subdomain();}catch(e){result.error=true}process.stdout.write(JSON.stringify(result));`)
			cmd.Stdin = strings.NewReader(string(payload))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("JavaScript bootstrap: %s %v", out, err)
			}
			var result struct {
				Loads           int
				Base, Subdomain string
				Error           bool
			}
			if err := json.Unmarshal(out, &result); err != nil {
				t.Fatal(err)
			}
			if (result.Loads == 1) != tc.wantLoad || result.Error != tc.wantError || (!tc.wantError && (result.Base != tc.wantBase || result.Subdomain != tc.wantSubdomain)) {
				t.Fatalf("unexpected runtime selection: %+v", result)
			}
		})
	}
}

func TestExistingGISSBuildRemainsOptOut(t *testing.T) {
	t.Setenv("REMENTOR_API_ORIGIN", "")
	markup := `<html><head></head></html>`
	if injectRuntimeOrigins(markup) != markup || runtimeBaseExpression("http://dev.giss.localhost") != `"http://dev.giss.localhost"` {
		t.Fatal("existing builds changed without runtime-origin configuration")
	}
}
