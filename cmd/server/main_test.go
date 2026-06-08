package main

import "testing"

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
