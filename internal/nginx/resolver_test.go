package nginx

import (
	"reflect"
	"testing"
)

func TestResolversFromConfig(t *testing.T) {
	got := resolversFromConfig("# managed\nsearch private.test\nnameserver 127.0.0.53\nnameserver fd7a:115c:a1e0::53 # tailnet\nnameserver 127.0.0.53\nnameserver bad;directive\n")
	want := []string{"127.0.0.53", "[fd7a:115c:a1e0::53]"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolvers = %v, want %v", got, want)
	}
	if got := resolversFromConfig(""); len(got) != 0 {
		t.Fatalf("unexpected public DNS fallback: %v", got)
	}
}
