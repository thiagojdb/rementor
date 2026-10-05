// Package access defines public origins and device-to-workspace selection.
// It contains no upstream addresses or management credentials.
package access

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/thiagojdb/rementor/internal/validation"
)

const SchemaVersion = 1
const MaxDocumentBytes = 1 << 20

var ErrNotAuthorized = errors.New("device is not authorized for application access")
var ErrNotPublished = errors.New("public origin is not published for this workspace")

type Origin struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}
type Binding struct {
	WorkspaceID string `json:"workspaceId"`
	OriginID    string `json:"originId"`
	LocalHost   string `json:"localHost"`
}
type Device struct {
	NodeID      string `json:"nodeId"`
	WorkspaceID string `json:"workspaceId"`
}
type Document struct {
	SchemaVersion     int       `json:"schemaVersion"`
	Origins           []Origin  `json:"origins"`
	Bindings          []Binding `json:"bindings"`
	Devices           []Device  `json:"devices"`
	SharedWorkspaceID string    `json:"sharedWorkspaceId"`
	AllowedUserIDs    []uint64  `json:"allowedUserIds"`
}
type Configuration struct {
	Revision uint64   `json:"revision"`
	Document Document `json:"document"`
}
type Identity struct {
	NodeID string
	UserID uint64
	Tagged bool
}
type Selection struct {
	WorkspaceID string `json:"workspaceId"`
	Source      string `json:"source"`
}
type Manifest struct {
	SchemaVersion    int               `json:"schemaVersion"`
	Revision         uint64            `json:"revision,string"`
	WorkspaceID      string            `json:"workspaceId"`
	EnvironmentID    string            `json:"environmentId"`
	SessionID        string            `json:"sessionId,omitempty"`
	SelectionSource  string            `json:"selectionSource"`
	RouteVersion     uint64            `json:"routeVersion,string"`
	Origins          map[string]string `json:"origins"`
	RoutingHosts     map[string]string `json:"routingHosts"`
	WebSocketOrigins map[string]string `json:"webSocketOrigins"`
}

func Empty() Document { return Document{SchemaVersion: SchemaVersion} }
func (doc Document) Clone() Document {
	doc.Origins = append([]Origin(nil), doc.Origins...)
	doc.Bindings = append([]Binding(nil), doc.Bindings...)
	doc.Devices = append([]Device(nil), doc.Devices...)
	doc.AllowedUserIDs = append([]uint64(nil), doc.AllowedUserIDs...)
	return doc
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
var nodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// CanonicalOrigin rejects credentials, paths and browser-ambiguous authorities.
// Plain HTTP is available only for loopback development origins.
func CanonicalOrigin(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.ForceQuery {
		return "", errors.New("public URL must be an HTTP(S) origin without credentials, path, query or fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.ContainsAny(raw, "\r\n\t \\") || strings.HasSuffix(host, ".") {
		return "", errors.New("invalid public origin")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		if len(host) > 253 {
			return "", errors.New("invalid public hostname")
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) > 63 || !dnsLabel.MatchString(label) {
				return "", errors.New("invalid public hostname")
			}
		}
	}
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !(host == "localhost" || strings.HasSuffix(host, ".localhost") || (ip != nil && ip.IsLoopback()))) {
		return "", errors.New("public origins require HTTPS; HTTP is permitted only on localhost")
	}
	port := parsed.Port()
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", errors.New("invalid public origin port")
		}
		port = strconv.Itoa(value)
	}
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if port != "" {
		authority = net.JoinHostPort(host, port)
	}
	// url.Parse can accept an empty explicit port. Reject that alternate spelling.
	if strings.HasSuffix(parsed.Host, ":") {
		return "", errors.New("invalid public origin port")
	}
	return parsed.Scheme + "://" + authority, nil
}
func (doc Document) Validate() error {
	if doc.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported public access schema %d", doc.SchemaVersion)
	}
	if len(doc.Origins) > 64 || len(doc.Bindings) > 4096 || len(doc.Devices) > 128 || len(doc.AllowedUserIDs) > 64 {
		return errors.New("public access configuration is too large")
	}
	if len(doc.Origins) == 0 {
		if len(doc.Bindings) != 0 || len(doc.Devices) != 0 || len(doc.AllowedUserIDs) != 0 || doc.SharedWorkspaceID != "" {
			return errors.New("configure public origins before workspace or device selection")
		}
		return nil
	}
	origins := map[string]string{}
	for _, origin := range doc.Origins {
		if err := validation.Identifier("origin ID", origin.ID); err != nil {
			return err
		}
		if _, exists := origins[origin.ID]; exists {
			return fmt.Errorf("duplicate origin %q", origin.ID)
		}
		canonical, err := CanonicalOrigin(origin.URL)
		if err != nil {
			return fmt.Errorf("origin %s: %w", origin.ID, err)
		}
		origins[origin.ID] = canonical
	}
	scopes := map[string]map[string]string{}
	authorities := map[string]string{}
	for _, binding := range doc.Bindings {
		if err := validation.Identifier("workspace ID", binding.WorkspaceID); err != nil {
			return err
		}
		if err := validation.LocalHostname(binding.LocalHost); err != nil {
			return err
		}
		origin, exists := origins[binding.OriginID]
		if !exists {
			return fmt.Errorf("binding references unknown origin %q", binding.OriginID)
		}
		if scopes[binding.WorkspaceID] == nil {
			scopes[binding.WorkspaceID] = map[string]string{}
		}
		if _, exists := scopes[binding.WorkspaceID][binding.OriginID]; exists {
			return errors.New("duplicate workspace origin binding")
		}
		scopes[binding.WorkspaceID][binding.OriginID] = binding.LocalHost
		key := binding.WorkspaceID + "/" + origin
		if host, exists := authorities[key]; exists && host != binding.LocalHost {
			return errors.New("equivalent public origins must use the same host in a workspace")
		}
		authorities[key] = binding.LocalHost
	}
	for workspace, bindings := range scopes {
		if len(bindings) != len(origins) {
			return fmt.Errorf("workspace %q must bind every configured origin", workspace)
		}
	}
	if scopes[doc.SharedWorkspaceID] == nil {
		return errors.New("choose a published shared workspace")
	}
	seen := map[string]bool{}
	for _, device := range doc.Devices {
		if !nodePattern.MatchString(device.NodeID) || seen[device.NodeID] {
			return errors.New("invalid or duplicate device node ID")
		}
		seen[device.NodeID] = true
		if scopes[device.WorkspaceID] == nil {
			return fmt.Errorf("device %q selects an unpublished workspace", device.NodeID)
		}
	}
	users := map[uint64]bool{}
	for _, user := range doc.AllowedUserIDs {
		if user == 0 || users[user] {
			return errors.New("invalid or duplicate allowed Tailscale user ID")
		}
		users[user] = true
	}
	if len(users) == 0 {
		return errors.New("allow at least one Tailscale user for shared access")
	}
	raw, _ := json.Marshal(doc)
	if len(raw) > MaxDocumentBytes {
		return errors.New("public access configuration exceeds 1 MiB")
	}
	return nil
}
func Decode(reader io.Reader) (Document, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, MaxDocumentBytes+1))
	if err != nil {
		return Document{}, err
	}
	if len(raw) > MaxDocumentBytes {
		return Document{}, errors.New("public access document exceeds 1 MiB")
	}
	var doc Document
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return doc, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return doc, errors.New("public access document must contain one JSON object")
	}
	return doc, doc.Validate()
}
func (doc Document) Select(identity Identity) (Selection, error) {
	if !nodePattern.MatchString(identity.NodeID) {
		return Selection{}, ErrNotAuthorized
	}
	allowed := false
	for _, id := range doc.AllowedUserIDs {
		if id == identity.UserID && id != 0 {
			allowed = true
			break
		}
	}
	for _, device := range doc.Devices {
		if device.NodeID == identity.NodeID {
			// Tagged machines have no human user identity. Only an explicitly selected
			// tagged node can use access; user-owned devices must also match the user list.
			if !allowed && !identity.Tagged {
				return Selection{}, ErrNotAuthorized
			}
			return Selection{WorkspaceID: device.WorkspaceID, Source: "device"}, nil
		}
	}
	if identity.Tagged || !allowed || doc.SharedWorkspaceID == "" {
		return Selection{}, ErrNotAuthorized
	}
	return Selection{WorkspaceID: doc.SharedWorkspaceID, Source: "shared"}, nil
}
func (doc Document) OriginsFor(workspace string) (map[string]string, map[string]string, error) {
	origins := map[string]string{}
	sockets := map[string]string{}
	configured := map[string]string{}
	for _, origin := range doc.Origins {
		canonical, err := CanonicalOrigin(origin.URL)
		if err != nil {
			return nil, nil, err
		}
		configured[origin.ID] = canonical
	}
	for _, binding := range doc.Bindings {
		if binding.WorkspaceID != workspace {
			continue
		}
		value := configured[binding.OriginID]
		origins[binding.OriginID] = value
		sockets[binding.OriginID] = "ws" + strings.TrimPrefix(value, "http")
	}
	if len(origins) == 0 {
		return nil, nil, ErrNotPublished
	}
	return origins, sockets, nil
}
func (doc Document) LocalHost(workspace, publicOrigin string) (string, error) {
	canonical, err := CanonicalOrigin(publicOrigin)
	if err != nil {
		return "", err
	}
	for _, origin := range doc.Origins {
		key, _ := CanonicalOrigin(origin.URL)
		if key != canonical {
			continue
		}
		for _, binding := range doc.Bindings {
			if binding.WorkspaceID == workspace && binding.OriginID == origin.ID {
				return binding.LocalHost, nil
			}
		}
	}
	return "", ErrNotPublished
}
