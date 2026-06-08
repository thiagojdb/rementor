package cli

import (
	"flag"
	"strings"
)

type boolFlag interface {
	IsBoolFlag() bool
}

// parseFlags accepts flags before or after positional arguments. The standard
// flag package stops at the first positional argument, which is surprising for
// commands such as "app register <workspace> <app> --port 8080".
func parseFlags(fs *flag.FlagSet, args []string) error {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		flags = append(flags, arg)
		nameAndValue := strings.TrimLeft(arg, "-")
		name, _, hasValue := strings.Cut(nameAndValue, "=")
		defined := fs.Lookup(name)
		if defined == nil || hasValue {
			continue
		}
		if candidate, ok := defined.Value.(boolFlag); ok && candidate.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}

	return fs.Parse(append(flags, positionals...))
}
