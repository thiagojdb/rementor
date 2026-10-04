package nginx

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type routeProofEntry struct {
	Pattern    string
	ProofIndex int
}
type routeProofMap struct {
	Name    string
	Entries []routeProofEntry
}

// Coalesce routes with identical proxy behavior. A URI map retains each
// original route's proof. Routes that overlap a different behavior stay as
// explicit nginx locations, preserving exact/longest-prefix precedence.
func compactLocations(cfg *Config) {
	for si := range cfg.Servers {
		server := &cfg.Servers[si]
		groups := make(map[Proxy][]int)
		var order []Proxy
		for i, loc := range server.Locations {
			if loc.Pattern == "/" || loc.Redirect != "" || loc.Rewrite != "" || loc.Trace || loc.StripOrigin || loc.Proxy.PassURI != "" {
				continue
			}
			if _, ok := groups[loc.Proxy]; !ok {
				order = append(order, loc.Proxy)
			}
			groups[loc.Proxy] = append(groups[loc.Proxy], i)
		}
		removed := make(map[int]bool)
		var merged []Location
		for _, proxy := range order {
			indexes := groups[proxy]
			members := make(map[int]bool)
			for _, i := range indexes {
				members[i] = true
			}
			var safe []int
			for _, i := range indexes {
				overlap := false
				for j, other := range server.Locations {
					if members[j] || (other.Pattern == "/" && other.Modifier == "") {
						continue
					}
					if locationsOverlap(server.Locations[i], other) {
						overlap = true
						break
					}
				}
				if !overlap {
					safe = append(safe, i)
				}
			}
			if len(safe) < 4 {
				continue
			}
			sort.SliceStable(safe, func(i, j int) bool {
				a, b := server.Locations[safe[i]], server.Locations[safe[j]]
				if (a.Modifier == "=") != (b.Modifier == "=") {
					return a.Modifier == "="
				}
				return len(a.Pattern) > len(b.Pattern)
			})
			name := fmt.Sprintf("rementor_location_%d_%d", si, len(merged))
			proofMap := routeProofMap{Name: name}
			var alternatives []string
			for _, i := range safe {
				loc := server.Locations[i]
				pattern := regexp.QuoteMeta(loc.Pattern)
				if loc.Modifier == "=" {
					pattern += "$"
				}
				proofMap.Entries = append(proofMap.Entries, routeProofEntry{Pattern: "^" + pattern, ProofIndex: loc.ProofIndex})
				alternatives = append(alternatives, pattern)
				removed[i] = true
			}
			loc := server.Locations[safe[0]]
			loc.Modifier = "~"
			loc.Pattern = "^(?:" + strings.Join(alternatives, "|") + ")"
			loc.ProofSelector = "$" + name
			merged = append(merged, loc)
			cfg.RouteMaps = append(cfg.RouteMaps, proofMap)
		}
		var kept []Location
		for i, loc := range server.Locations {
			if !removed[i] {
				kept = append(kept, loc)
			}
		}
		server.Locations = append(kept, merged...)
	}
}

func locationsOverlap(a, b Location) bool {
	if a.Modifier == "=" && b.Modifier == "=" {
		return a.Pattern == b.Pattern
	}
	if a.Modifier == "=" {
		return strings.HasPrefix(a.Pattern, b.Pattern)
	}
	if b.Modifier == "=" {
		return strings.HasPrefix(b.Pattern, a.Pattern)
	}
	return strings.HasPrefix(a.Pattern, b.Pattern) || strings.HasPrefix(b.Pattern, a.Pattern)
}
