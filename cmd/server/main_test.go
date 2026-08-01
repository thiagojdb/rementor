package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if !isLoopbackHost(host) {
			t.Errorf("expected %q to be accepted", host)
		}
	}
	for _, host := range []string{"0.0.0.0", "192.0.2.10", "example.test", ""} {
		if isLoopbackHost(host) {
			t.Errorf("expected %q to be rejected", host)
		}
	}
}

func TestLoadFrontendAssets(t *testing.T) {
	distPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(distPath, "index.html"), []byte("<meta name=csrf content=\"%REMENTOR_CSRF_TOKEN%\">"), 0o644); err != nil {
		t.Fatal(err)
	}

	assetFS, indexData, err := loadFrontendAssets(distPath, "csrf-token")
	if err != nil {
		t.Fatalf("loadFrontendAssets() error = %v", err)
	}
	if assetFS == nil {
		t.Fatal("loadFrontendAssets() returned a nil filesystem")
	}
	if got := string(indexData); !strings.Contains(got, "csrf-token") || strings.Contains(got, "%REMENTOR_CSRF_TOKEN%") {
		t.Fatalf("indexData = %q, expected CSRF placeholder replacement", got)
	}
}

func TestLoadFrontendAssetsMissingIndex(t *testing.T) {
	_, _, err := loadFrontendAssets(t.TempDir(), "csrf-token")
	if err == nil {
		t.Fatal("loadFrontendAssets() error = nil, want missing index error")
	}
	if !strings.Contains(err.Error(), frontendDistEnv) {
		t.Fatalf("loadFrontendAssets() error = %q, want %s guidance", err, frontendDistEnv)
	}
}
