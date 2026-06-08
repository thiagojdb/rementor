package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
)

// Die prints an error message to stderr and exits with code 1.
func Die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

// PrintJSON encodes v as indented JSON to stdout.
func PrintJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		Die("failed to encode JSON: %v", err)
	}
}

// NewTabWriter returns a tab writer suitable for table output.
func NewTabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}
