package models

import "testing"

func TestClampInt32(t *testing.T) {
	if got := ClampInt32(-1 << 40); got != -1<<31 {
		t.Fatalf("ClampInt32(min) = %d, want %d", got, -1<<31)
	}
	if got := ClampInt32(1 << 40); got != 1<<31-1 {
		t.Fatalf("ClampInt32(max) = %d, want %d", got, 1<<31-1)
	}
	if got := ClampInt32(42); got != 42 {
		t.Fatalf("ClampInt32(42) = %d, want 42", got)
	}
}

func TestApplicationRemoteHealthURLUsesContext(t *testing.T) {
	app := &Application{
		Path:    "/orders",
		Context: "/service-orders",
		Health:  DefaultHealthEndpoint,
	}

	got := app.RemoteHealthURL("https://api.remote.example.test/")
	want := "https://api.remote.example.test/service-orders/actuator/health"

	if got != want {
		t.Fatalf("expected remote health URL %q, got %q", want, got)
	}
}

func TestApplicationRemoteHealthURLFallsBackToPath(t *testing.T) {
	app := &Application{
		Path:   "/orders",
		Health: DefaultHealthEndpoint,
	}

	got := app.RemoteHealthURL("https://api.remote.example.test")
	want := "https://api.remote.example.test/orders/actuator/health"

	if got != want {
		t.Fatalf("expected remote health URL %q, got %q", want, got)
	}
}

func TestNormalizeIdentityTokenAndAliases(t *testing.T) {
	app := Application{ID: "rtc", Aliases: []string{" Front_GISS-V2 ", "rtc", "front-giss-v2"}}
	if got, want := NormalizeIdentityToken(" Reforma Tributaria_Consumo "), "reforma-tributaria-consumo"; got != want {
		t.Fatalf("normalized identity = %q, want %q", got, want)
	}
	aliases := app.NormalizedAliases()
	if len(aliases) != 1 || aliases[0] != "front-giss-v2" {
		t.Fatalf("normalized aliases = %#v", aliases)
	}
}
