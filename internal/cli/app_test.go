package cli

import "testing"

func TestUpsertAppPreservesExistingValuesWhenOptionalFieldsOmitted(t *testing.T) {
	existing := []ApplicationDTO{{
		ID:            "orders-api",
		Name:          "Orders API",
		Path:          "/orders",
		RemoteBaseUrl: "http://127.0.0.1:18080",
		Context:       "/orders",
		Port:          28081,
		Health:        "health",
	}}

	updated, status := upsertApp(existing, ApplicationConfigInput{
		ID:     "orders-api",
		Path:   "/orders",
		Port:   28082,
		Health: "health",
	})

	if status != "updated" {
		t.Fatalf("expected updated status, got %q", status)
	}
	if len(updated) != 1 {
		t.Fatalf("expected one app, got %d", len(updated))
	}
	got := updated[0]
	if got.Name != "Orders API" {
		t.Fatalf("expected existing name to be preserved, got %q", got.Name)
	}
	if got.RemoteBaseUrl != "http://127.0.0.1:18080" {
		t.Fatalf("expected existing remote base URL to be preserved, got %q", got.RemoteBaseUrl)
	}
	if got.Context != "/orders" {
		t.Fatalf("expected existing context to be preserved, got %q", got.Context)
	}
	if got.Port != 28082 {
		t.Fatalf("expected updated port, got %d", got.Port)
	}
}

func TestUpsertAppReportsUnchangedForIdenticalConfig(t *testing.T) {
	existing := []ApplicationDTO{{
		ID:      "billing-api",
		Name:    "Billing API",
		Path:    "/billing",
		Context: "/billing",
		Port:    28082,
		Health:  "health",
	}}

	updated, status := upsertApp(existing, ApplicationConfigInput{
		ID:      "billing-api",
		Name:    "Billing API",
		Path:    "/billing",
		Context: "/billing",
		Port:    28082,
		Health:  "health",
	})

	if status != "unchanged" {
		t.Fatalf("expected unchanged status, got %q", status)
	}
	if updated != nil {
		t.Fatalf("expected nil updated list for unchanged app, got %#v", updated)
	}
}

func TestUpsertAppPreservesOmittedRouteOverrideAndAcceptsExplicitFalse(t *testing.T) {
	existing := []ApplicationDTO{{
		ID: "orders-api", Path: "/orders", Port: 28082, Health: "health", RouteOverride: true,
	}}

	updated, status := upsertApp(existing, ApplicationConfigInput{ID: "orders-api", Path: "/orders", Port: 28083, Health: "health"})
	if status != "updated" || len(updated) != 1 || updated[0].RouteOverride == nil || !*updated[0].RouteOverride {
		t.Fatalf("omitted route override was not preserved: status=%q updated=%#v", status, updated)
	}

	updated, status = upsertApp(existing, ApplicationConfigInput{ID: "orders-api", Path: "/orders", Port: 28083, Health: "health", RouteOverride: boolPtr(false)})
	if status != "updated" || len(updated) != 1 || updated[0].RouteOverride == nil || *updated[0].RouteOverride {
		t.Fatalf("explicit false route override was not retained: status=%q updated=%#v", status, updated)
	}
}
