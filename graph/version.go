package graph

import (
	"strconv"
	"strings"
)

// Version is an optional per-feature version tag. Comparison rules (documented
// in docs/versioning.md):
//
//   - empty versions are equal to each other and lower than any non-empty;
//   - dotted numeric components compare numerically: "1.10" > "1.9";
//   - components that are not integers for both sides compare lexicographically;
//   - longer numeric dotted versions win when all shared components equal
//     ("1.2.1" > "1.2").
func CompareVersions(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	n := len(pa)
	if len(pb) < n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		ai, aerr := strconv.Atoi(pa[i])
		bi, berr := strconv.Atoi(pb[i])
		switch {
		case aerr == nil && berr == nil:
			if ai != bi {
				if ai < bi {
					return -1
				}
				return 1
			}
		default:
			if pa[i] != pb[i] {
				return strings.Compare(pa[i], pb[i])
			}
		}
	}
	switch {
	case len(pa) < len(pb):
		return -1
	case len(pa) > len(pb):
		return 1
	}
	return 0
}
