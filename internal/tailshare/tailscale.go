package tailshare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"reflect"
	"strings"
	"time"

	"github.com/thiagojdb/rementor/internal/access"
)

// Tailscale uses the local API's ETag when editing Serve, preserving all
// unrelated fields and rejecting concurrent writes. Credentials never enter
// the access document, database, browser response or command line.
type Tailscale struct {
	socket          string
	local           *http.Client
	cloud           *http.Client
	probe           *http.Client
	apiBase         string
	token           string
	ApplicationPath func(access.Manifest, Origin) string
}

func NewTailscale(socket, token string) *Tailscale {
	if socket == "" {
		socket = "/var/run/tailscale/tailscaled.sock"
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
	}}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Tailscale{socket: socket, token: token, apiBase: "https://api.tailscale.com/api/v2/tailnet/-/vip-services/", local: &http.Client{Transport: transport, Timeout: 5 * time.Second}, cloud: &http.Client{Timeout: 8 * time.Second, CheckRedirect: noRedirect}, probe: &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second, CheckRedirect: noRedirect}}
}
func (t *Tailscale) Close() {
	t.local.CloseIdleConnections()
	t.cloud.CloseIdleConnections()
	t.probe.CloseIdleConnections()
}
func (t *Tailscale) localRequest(ctx context.Context, method, path string, body []byte, etag string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://local-tailscaled.sock/localapi/v0/"+path, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	if method != "GET" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", etag)
	}
	response, err := t.local.Do(req)
	if err != nil {
		return nil, "", errors.New("Tailscale gateway is unavailable; connect its local socket")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusPreconditionFailed {
		return nil, "", errors.New("Tailscale configuration changed concurrently; retrying")
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("Tailscale local operation failed (HTTP %d); grant the Rementor user operator access to the gateway", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil {
		return nil, "", err
	}
	if len(raw) > 8<<20 {
		return nil, "", errors.New("Tailscale response is too large")
	}
	return raw, response.Header.Get("ETag"), nil
}
func (t *Tailscale) Inspect(ctx context.Context) (Gateway, error) {
	raw, _, err := t.localRequest(ctx, "GET", "status", nil, "")
	if err != nil {
		return Gateway{}, err
	}
	var status struct {
		BackendState string
		CertDomains  []string
		Self         *struct {
			ID       string
			HostName string
			Tags     []string
		}
		CurrentTailnet *struct {
			MagicDNSSuffix  string
			MagicDNSEnabled bool
		}
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return Gateway{}, errors.New("invalid Tailscale gateway status")
	}
	g := Gateway{CanCreate: t.token != ""}
	if status.BackendState != "Running" || status.Self == nil {
		g.Setup = "Connect the dedicated Tailscale gateway to your tailnet."
		return g, nil
	}
	g.Name = status.Self.HostName
	g.NodeID = status.Self.ID
	if g.NodeID == "" {
		return g, errors.New("Tailscale gateway identity is unavailable")
	}
	if status.CurrentTailnet == nil || !status.CurrentTailnet.MagicDNSEnabled {
		g.Setup = "Enable MagicDNS in the Tailscale DNS settings."
		return g, nil
	}
	g.DNSSuffix = strings.TrimSuffix(status.CurrentTailnet.MagicDNSSuffix, ".")
	if !strings.HasSuffix(g.DNSSuffix, ".ts.net") || strings.Count(g.DNSSuffix, ".") != 2 {
		return g, errors.New("invalid Tailscale DNS suffix")
	}
	if len(status.Self.Tags) == 0 {
		g.Setup = "Named Services needs a tagged gateway. Connect a dedicated tagged Tailscale gateway; keep your everyday computer’s identity."
	} else if len(status.CertDomains) == 0 {
		g.Setup = "Enable HTTPS certificates in the Tailscale DNS settings."
	}
	return g, nil
}
func serviceConfig(o Origin) map[string]any {
	u, _ := url.Parse(o.URL)
	return map[string]any{"TCP": map[string]any{"443": map[string]any{"HTTPS": true}}, "Web": map[string]any{u.Hostname() + ":443": map[string]any{"Handlers": map[string]any{"/": map[string]any{"Proxy": o.Target}}}}}
}
func (t *Tailscale) serve(ctx context.Context) (map[string]any, string, error) {
	raw, etag, err := t.localRequest(ctx, "GET", "serve-config", nil, "")
	if err != nil {
		return nil, "", err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, "", errors.New("invalid Tailscale Serve configuration")
	}
	if document == nil {
		document = map[string]any{}
	}
	if etag == "" {
		return nil, "", errors.New("Tailscale must support versioned Serve updates")
	}
	return document, etag, nil
}
func services(document map[string]any) (map[string]any, error) {
	if document["Services"] == nil {
		entries := map[string]any{}
		document["Services"] = entries
		return entries, nil
	}
	entries, ok := document["Services"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid Serve Services configuration")
	}
	return entries, nil
}
func (t *Tailscale) Ensure(ctx context.Context, o Origin, owner Ownership, claim func() error) error {
	doc, etag, err := t.serve(ctx)
	if err != nil {
		return err
	}
	entries, err := services(doc)
	if err != nil {
		return err
	}
	wanted := serviceConfig(o)
	existing, exists := entries["svc:"+o.Name]
	if exists && (!owner.Claimed || !reflect.DeepEqual(existing, wanted)) {
		return errors.New("service name already has another Serve configuration; choose another name")
	}
	if err := t.ensureDefinition(ctx, o, owner); err != nil {
		return err
	}
	// Persist a claim before any local network mutation. Recovery can then
	// remove a partially configured publication after a crash or cancellation.
	if err := claim(); err != nil {
		return err
	}
	if !exists {
		entries["svc:"+o.Name] = wanted
		raw, _ := json.Marshal(doc)
		if _, _, err := t.localRequest(ctx, "POST", "serve-config", raw, etag); err != nil {
			return err
		}
	}
	return t.advertise(ctx, "advertise", o.Name)
}
func (t *Tailscale) Remove(ctx context.Context, o Origin) error {
	doc, etag, err := t.serve(ctx)
	if err != nil {
		return err
	}
	entries, err := services(doc)
	if err != nil {
		return err
	}
	existing, exists := entries["svc:"+o.Name]
	if exists {
		if !reflect.DeepEqual(existing, serviceConfig(o)) {
			return errors.New("owned Serve entry was changed externally; left it untouched")
		}
		delete(entries, "svc:"+o.Name)
		raw, _ := json.Marshal(doc)
		if _, _, err := t.localRequest(ctx, "POST", "serve-config", raw, etag); err != nil {
			return err
		}
	}
	return t.advertise(ctx, "drain", o.Name)
}
func (t *Tailscale) advertise(ctx context.Context, action, name string) error {
	// No shell, sudo, credentials or provider output: errors can contain login
	// links and must not be returned to the browser or logs.
	command := exec.CommandContext(ctx, "tailscale", "--socket="+t.socket, "serve", action, "svc:"+name)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("Tailscale could not %s the named service; check gateway operator permissions and service approval", action)
	}
	return nil
}
func (t *Tailscale) ensureDefinition(ctx context.Context, o Origin, owner Ownership) error {
	if t.token == "" {
		raw, _, err := t.localRequest(ctx, "GET", "services", nil, "")
		if err != nil {
			return err
		}
		var catalog map[string]struct{ Ports []string }
		if err := json.Unmarshal(raw, &catalog); err != nil {
			return errors.New("invalid Tailscale Services catalog")
		}
		entry, ok := catalog["svc:"+o.Name]
		if !ok {
			return fmt.Errorf("Create the internal service svc:%s with endpoint tcp:443 in Tailscale, or configure a Services API token for automatic creation", o.Name)
		}
		for _, port := range entry.Ports {
			if port == "tcp:443" {
				return nil
			}
		}
		return errors.New("the existing Tailscale service must advertise tcp:443")
	}
	path := t.apiBase + url.PathEscape("svc:"+o.Name)
	request := func(method string, body []byte) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(t.token, "")
		req.Header.Set("Content-Type", "application/json")
		return t.cloud.Do(req)
	}
	res, err := request("GET", nil)
	if err != nil {
		return errors.New("Tailscale service administration is unavailable")
	}
	if res.StatusCode == http.StatusOK {
		defer res.Body.Close()
		var service map[string]any
		if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&service); err != nil {
			return errors.New("invalid service definition")
		}
		annotations, _ := service["annotations"].(map[string]any)
		currentOwner, _ := annotations["rementor-owner"].(string)
		ports, _ := service["ports"].([]any)
		validPort := false
		for _, port := range ports {
			if port == "tcp:443" {
				validPort = true
			}
		}
		if !validPort {
			return errors.New("managed service endpoint was changed externally; restore tcp:443")
		}
		if currentOwner != owner.Publication {
			if !owner.Claimed || currentOwner != owner.Installation {
				return errors.New("service name belongs to another publication; choose another name or use its shared ingress")
			}
			// The original gateway can promote its own definition into a
			// shared group. Preserve allocated addresses and every other field.
			annotations["rementor-owner"] = owner.Publication
			raw, _ := json.Marshal(service)
			updated, err := request("PUT", raw)
			if err != nil {
				return errors.New("could not attach service to shared ingress")
			}
			defer updated.Body.Close()
			if updated.StatusCode != http.StatusOK {
				return errors.New("could not attach service to shared ingress")
			}
		}
		return nil
	}
	code := res.StatusCode
	res.Body.Close()
	if code != http.StatusNotFound {
		return fmt.Errorf("Tailscale service lookup failed (HTTP %d); check Services API permissions", code)
	}
	body, _ := json.Marshal(map[string]any{"name": "svc:" + o.Name, "ports": []string{"tcp:443"}, "comment": "Managed by Rementor", "annotations": map[string]string{"rementor-owner": owner.Publication}})
	res, err = request("PUT", body)
	if err != nil {
		return errors.New("Tailscale service creation is unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Tailscale service creation failed (HTTP %d); check Services API permissions", res.StatusCode)
	}
	return nil
}

// Verify checks real DNS/TLS and the authenticated runtime contract. It does
// not treat a saved URL, a generic 200, redirects, or a denied request as ready.
func (t *Tailscale) Verify(ctx context.Context, o Origin, policy access.Configuration) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", o.URL+"/__rementor/origins", nil)
	res, err := t.probe.Do(req)
	if err != nil {
		return errors.New("Waiting for service approval, DNS and HTTPS to become reachable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTPS responds with %d; verify service approval and allow this computer’s Tailscale account in access settings", res.StatusCode)
	}
	var manifest access.Manifest
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&manifest); err != nil || manifest.SchemaVersion != access.SchemaVersion {
		return errors.New("HTTPS did not return a Rementor application manifest")
	}
	expected, _, err := policy.Document.OriginsFor(manifest.WorkspaceID)
	if err != nil || manifest.Revision != policy.Revision || !reflect.DeepEqual(expected, manifest.Origins) {
		return errors.New("service reached a different or stale access policy")
	}
	found := false
	for _, origin := range manifest.Origins {
		if origin == o.URL {
			found = true
		}
	}
	if !found {
		return errors.New("service reached another application")
	}
	path := "/"
	if t.ApplicationPath != nil {
		path = t.ApplicationPath(manifest, o)
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\\r\n") {
		return errors.New("application verification path is unavailable")
	}
	applicationRequest, _ := http.NewRequestWithContext(ctx, "GET", o.URL+path, nil)
	application, err := t.probe.Do(applicationRequest)
	if err != nil {
		return errors.New("application request is unavailable")
	}
	defer application.Body.Close()
	appID := application.Header.Get("X-Rementor-App-ID")
	if application.StatusCode < 200 || application.StatusCode >= 500 || application.Header.Get("X-Rementor-Workspace") != manifest.WorkspaceID || appID == "" || appID == "unknown" {
		return errors.New("HTTPS is reachable, but the selected application route is not verified")
	}
	return nil
}
