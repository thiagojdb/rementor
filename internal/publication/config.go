// Package publication owns hostname-pinned tailnet publication. It does not
// change routing intent or the older device-selected public access policy.
package publication

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/tailnode"
	"github.com/thiagojdb/rementor/internal/validation"
)

var ErrConflict = errors.New("tailnet publishing changed; reload and try again")

type Entry struct {
	Computer        string `json:"computer"`
	Name            string `json:"name"`
	WorkspaceID     string `json:"workspaceId"`
	IncludeSessions bool   `json:"includeSessions"`
}
type Configuration struct {
	Revision        uint64  `json:"revision"`
	Enabled         bool    `json:"enabled"`
	Suffix          string  `json:"suffix"`
	CertificateMode string  `json:"certificateMode"`
	ListenAddress   string  `json:"listenAddress"`
	NodeName        string  `json:"nodeName,omitempty"`
	Entries         []Entry `json:"entries"`
}
type Scope struct {
	WorkspaceID   string
	Name          string
	EnvironmentID string
	SessionID     string
	Hosts         []string
}
type Link struct {
	LocalHost string `json:"localHost"`
	URL       string `json:"url"`
}

// Request crosses only the authenticated paired data plane, never a browser
// supplied forwarding header. Each routing owner verifies identity and hosts.
type Request struct {
	Computer      string `json:"computer"`
	WorkspaceID   string `json:"workspaceId"`
	SourceAddress string `json:"sourceAddress"`
	Links         []Link `json:"links"`
}
type PublishedScope struct {
	Entry Entry
	Scope Scope
	Links []Link
	Error string
}
type Store interface {
	Load() (Configuration, error)
	Save(Configuration, uint64) (Configuration, error)
}
type Catalog func(context.Context, string) ([]Scope, error)

func TailnetIP(ip net.IP) bool {
	_, v4, _ := net.ParseCIDR("100.64.0.0/10")
	_, v6, _ := net.ParseCIDR("fd7a:115c:a1e0::/48")
	return ip != nil && (v4.Contains(ip) || v6.Contains(ip))
}
func (c Configuration) Clone() Configuration {
	c.Entries = append([]Entry(nil), c.Entries...)
	return c
}
func ValidSuffix(suffix string) bool {
	if suffix == "" || !strings.Contains(suffix, ".") || len(suffix) > 180 || suffix != strings.ToLower(suffix) || suffix == "localhost" || strings.HasSuffix(suffix, ".localhost") {
		return false
	}
	canonical, err := access.CanonicalOrigin("https://" + suffix)
	return err == nil && canonical == "https://"+suffix && net.ParseIP(suffix) == nil
}
func (c Configuration) Validate() error {
	if c.NodeName != "" && !tailnode.ValidName(c.NodeName) {
		return errors.New("enter a Tailscale device name using letters, numbers and hyphens")
	}
	if !ValidSuffix(c.Suffix) {
		return errors.New("enter a hostname suffix without a scheme, path or port")
	}
	if c.CertificateMode != "private" && c.CertificateMode != "external" {
		return errors.New("choose private CA or an installed HTTPS certificate")
	}
	if c.CertificateMode == "external" && (c.Suffix == "ts.net" || strings.HasSuffix(c.Suffix, ".ts.net")) {
		return errors.New("nested .ts.net names require private DNS and a private CA")
	}
	if c.ListenAddress != "" && !TailnetIP(net.ParseIP(c.ListenAddress)) {
		return errors.New("choose this computer's Tailscale address")
	}
	if len(c.Entries) > 256 {
		return errors.New("too many published environments")
	}
	seen := map[string]bool{}
	labels := map[string]string{}
	for _, e := range c.Entries {
		raw, err := hex.DecodeString(e.Computer)
		if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != e.Computer {
			return errors.New("invalid publishing computer identity")
		}
		if validation.Identifier("workspace ID", e.WorkspaceID) != nil {
			return errors.New("invalid publication workspace")
		}
		if len(e.Name) > 63 || strings.Contains(e.Name, ".") || validation.LocalHostname(e.Name+".localhost") != nil {
			return errors.New("computer URL name must be a DNS label")
		}
		key := e.Computer + "/" + e.WorkspaceID
		if seen[key] {
			return errors.New("environment is already published")
		}
		seen[key] = true
		if owner, ok := labels[e.Name]; ok && owner != e.Computer {
			return fmt.Errorf("computer URL name %q is already used by another computer", e.Name)
		}
		labels[e.Name] = e.Computer
	}
	return nil
}

// Hostname replaces only the terminal localhost suffix. Existing dots,
// environment aliases and session prefixes are preserved exactly.
func Hostname(host, computer, suffix string) (string, error) {
	if !strings.HasSuffix(host, ".localhost") || validation.LocalHostname(host) != nil {
		return "", errors.New("publication requires a declared .localhost application host")
	}
	name := strings.TrimSuffix(host, ".localhost") + "." + computer + "." + suffix
	canonical, err := access.CanonicalOrigin("https://" + name)
	if err != nil || canonical != "https://"+name {
		return "", errors.New("generated hostname exceeds DNS limits")
	}
	return name, nil
}
func (r Request) Document(identity access.Identity) (access.Document, error) {
	doc := access.Empty()
	doc.SharedWorkspaceID = r.WorkspaceID
	if !TailnetIP(net.ParseIP(r.SourceAddress)) || len(r.Links) == 0 || len(r.Links) > 32 {
		return doc, errors.New("invalid published request")
	}
	for i, l := range r.Links {
		canonical, err := access.CanonicalOrigin(l.URL)
		if err != nil || canonical != l.URL || !strings.HasPrefix(l.URL, "https://") || strings.Contains(strings.TrimPrefix(l.URL, "https://"), ":") {
			return doc, errors.New("published URLs require HTTPS on port 443")
		}
		id := fmt.Sprintf("host-%d", i)
		doc.Origins = append(doc.Origins, access.Origin{ID: id, URL: l.URL})
		doc.Bindings = append(doc.Bindings, access.Binding{WorkspaceID: r.WorkspaceID, OriginID: id, LocalHost: l.LocalHost})
	}
	if identity.UserID != 0 {
		doc.AllowedUserIDs = []uint64{identity.UserID}
	} else {
		doc.AllowedUserIDs = []uint64{1}
	}
	// Entire-tailnet publication includes tagged devices, after WhoIs verifies
	// their current node identity. This selection cannot change the target.
	doc.Devices = []access.Device{{NodeID: identity.NodeID, WorkspaceID: r.WorkspaceID}}
	return doc, doc.Validate()
}
