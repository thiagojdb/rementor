package access

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func fixture() Document {
	return Document{SchemaVersion: 1, Origins: []Origin{{ID: "api", URL: "https://api.example.test"}, {ID: "frontend", URL: "https://frontend.example.test:8443"}}, Bindings: []Binding{{WorkspaceID: "shared", OriginID: "api", LocalHost: "shared.localhost"}, {WorkspaceID: "shared", OriginID: "frontend", LocalHost: "web.shared.localhost"}, {WorkspaceID: "branch", OriginID: "api", LocalHost: "branch.localhost"}, {WorkspaceID: "branch", OriginID: "frontend", LocalHost: "web.branch.localhost"}}, Devices: []Device{{NodeID: "device-a", WorkspaceID: "branch"}}, SharedWorkspaceID: "shared", AllowedUserIDs: []uint64{99}}
}
func TestPublicAccessSelectionKeepsOriginsStable(t *testing.T) {
	doc := fixture()
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	own, err := doc.Select(Identity{NodeID: "device-a", UserID: 99})
	if err != nil || own.WorkspaceID != "branch" || own.Source != "device" {
		t.Fatal("device did not select its explicit workspace")
	}
	phone, err := doc.Select(Identity{NodeID: "phone", UserID: 99})
	if err != nil || phone.WorkspaceID != "shared" || phone.Source != "shared" {
		t.Fatal("phone did not select explicit shared workspace")
	}
	first, sockets, err := doc.OriginsFor(own.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	second, _, _ := doc.OriginsFor(phone.WorkspaceID)
	if first["api"] != second["api"] || sockets["frontend"] != "wss://frontend.example.test:8443" {
		t.Fatal("device selection changed public origins")
	}
	if host, err := doc.LocalHost("branch", "https://API.example.test:443/"); err != nil || host != "branch.localhost" {
		t.Fatal("canonical URL failed to select local publication")
	}
	if _, err := doc.Select(Identity{NodeID: "device-a", UserID: 101}); !errors.Is(err, ErrNotAuthorized) {
		t.Fatal("node ID bypassed allowed human user")
	}
	if _, err := doc.Select(Identity{NodeID: "phone", UserID: 99, Tagged: true}); !errors.Is(err, ErrNotAuthorized) {
		t.Fatal("unknown tagged node acquired shared access")
	}
	if selection, err := doc.Select(Identity{NodeID: "device-a", Tagged: true}); err != nil || selection.WorkspaceID != "branch" {
		t.Fatal("explicit tagged node was rejected")
	}
	clone := doc.Clone()
	clone.Origins[0].URL = "https://changed.example.test"
	clone.Devices[0].WorkspaceID = "shared"
	clone.AllowedUserIDs[0] = 101
	if doc.Origins[0].URL == clone.Origins[0].URL || doc.Devices[0].WorkspaceID == clone.Devices[0].WorkspaceID || doc.AllowedUserIDs[0] == 101 {
		t.Fatal("document clone retained caller-owned slices")
	}
}
func TestPublicAccessRejectsAmbiguousOrUnsafeDocuments(t *testing.T) {
	for _, origin := range []string{"https://user:secret@api.example.test", "https://api.example.test/path", "https://api.example.test?token=private", "https://api.example.test#fragment", "https://api.example.test:", "https://api.example.test:0", "https://api.example.test:65536", "https://*.example.test", "https://api.example.test.", "http://api.example.test", "https://example.test\\evil"} {
		doc := fixture()
		doc.Origins[0].URL = origin
		if doc.Validate() == nil {
			t.Fatalf("accepted unsafe origin %q", origin)
		}
	}
	for _, change := range []func(*Document){func(d *Document) { d.SchemaVersion = 2 }, func(d *Document) { d.Bindings = d.Bindings[:3] }, func(d *Document) { d.Devices[0].WorkspaceID = "missing" }, func(d *Document) { d.AllowedUserIDs = nil }, func(d *Document) { d.Devices = append(d.Devices, d.Devices[0]) }, func(d *Document) { d.Bindings = append(d.Bindings, d.Bindings[0]) }} {
		doc := fixture()
		change(&doc)
		if doc.Validate() == nil {
			t.Fatal("accepted inconsistent access configuration")
		}
	}
	raw, _ := json.Marshal(fixture())
	if _, err := Decode(strings.NewReader(string(raw) + " {}")); err == nil {
		t.Fatal("accepted trailing document")
	}
	if _, err := Decode(strings.NewReader(strings.Replace(string(raw), `"schemaVersion":1`, `"schemaVersion":1,"unknown":true`, 1))); err == nil {
		t.Fatal("ignored unknown field")
	}
	if _, err := Decode(strings.NewReader(strings.Repeat(" ", MaxDocumentBytes+1))); err == nil {
		t.Fatal("accepted oversized document")
	}
	manifest := Manifest{Revision: 18446744073709551614, RouteVersion: 18446744073709551614}
	encoded, _ := json.Marshal(manifest)
	if !strings.Contains(string(encoded), `"revision":"18446744073709551614"`) || !strings.Contains(string(encoded), `"routeVersion":"18446744073709551614"`) {
		t.Fatal("manifest versions lost uint64 precision for browser consumers")
	}
}
