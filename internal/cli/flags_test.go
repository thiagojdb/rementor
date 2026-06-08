package cli

import (
	"flag"
	"testing"
)

func TestParseFlagsAcceptsFlagsAfterPositionals(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	port := fs.Int("port", 0, "")
	active := fs.Bool("active", false, "")

	if err := parseFlags(fs, []string{"demo", "orders-api", "--port", "8081", "--active"}); err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if *port != 8081 || !*active {
		t.Fatalf("flags were not parsed: port=%d active=%v", *port, *active)
	}
	if fs.NArg() != 2 || fs.Arg(0) != "demo" || fs.Arg(1) != "orders-api" {
		t.Fatalf("positionals were not preserved: %v", fs.Args())
	}
}

func TestParseFlagsAcceptsEqualsSyntax(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	port := fs.Int("port", 0, "")

	if err := parseFlags(fs, []string{"demo", "--port=8081"}); err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if *port != 8081 || fs.Arg(0) != "demo" {
		t.Fatalf("unexpected parse result: port=%d args=%v", *port, fs.Args())
	}
}
