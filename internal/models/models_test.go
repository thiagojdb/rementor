package models

import "testing"

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
