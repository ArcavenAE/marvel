// Package buildversion orders marvel version strings and holds the version
// stamped into the running build, which reads dev when it was not stamped.
package buildversion

import (
	"strconv"
	"strings"
)

// VersionOlder reports whether a is older than b, and whether the order is
// known at all. Both must parse as semantic versions (a dotted numeric core
// with an optional dash prerelease); a dev build or anything else has no order.
func VersionOlder(a, b string) (older, known bool) {
	ac, ap, aok := splitVersion(a)
	bc, bp, bok := splitVersion(b)
	if !aok || !bok {
		return false, false
	}
	for i := 0; i < len(ac) || i < len(bc); i++ {
		var x, y int
		if i < len(ac) {
			x = ac[i]
		}
		if i < len(bc) {
			y = bc[i]
		}
		if x != y {
			return x < y, true
		}
	}
	switch {
	case ap == "" && bp == "":
		return false, true
	case ap == "":
		return false, true
	case bp == "":
		return true, true
	}
	return comparePrerelease(ap, bp) < 0, true
}

// splitVersion parses "v1.2.3-pre.1+meta" into its numeric core and the
// prerelease. Build metadata is dropped.
func splitVersion(v string) (core []int, pre string, ok bool) {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")
	c, pre, _ := strings.Cut(v, "-")
	if c == "" {
		return nil, "", false
	}
	for _, p := range strings.Split(c, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, "", false
		}
		core = append(core, n)
	}
	return core, pre, true
}

// comparePrerelease orders two prerelease strings as semver does: dotted
// identifiers left to right, numbers numerically and below words, a shorter
// run below a longer one that begins the same.
func comparePrerelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return cmpInt(an, bn)
			}
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(as), len(bs))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// stamped is the version Set recorded; an unstamped build reads dev.
var stamped = "dev"

// Set records the version stamped into the binary (the main.version linker
// flag), once, from main. An empty value leaves the build unstamped.
//
// Owner reading, proposed for review before merge: the names Version and Set,
// and the wiring of the stamp through main.
func Set(v string) {
	if v != "" {
		stamped = v
	}
}

// Version is the running build's version string: the stamped value, or "dev"
// for a build that was not stamped.
func Version() string { return stamped }
