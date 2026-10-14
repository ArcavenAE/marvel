// Package seaturi is the one place marvel cuts a seat's identity string.
//
// A seat is named marvel://<cluster>/<ws>/<team>/<slot>, where the
// slot is <role>-<idx>. The generation and instance are liveness facts and
// never ride in the name, so a shift keeps the URI. Every identifier marvel
// stamps for a seat is cut from Build's output; a string assembled by hand
// elsewhere is a defect (aae-orc docs/design/seat-identity-grammar.md,
// section 3 and CAND-ID-6).
package seaturi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Scheme is the URI prefix of a seat identity.
const Scheme = "marvel://"

// Seat is the four names and the index that identify one slot.
type Seat struct {
	Cluster   string
	Workspace string
	Team      string
	Role      string
	Index     int
}

// Build returns the seat URI, or an error naming the first rule the seat
// breaks. A string that breaks a rule is rejected, never rewritten.
func Build(s Seat) (string, error) {
	for _, seg := range []struct{ name, value string }{
		{"cluster", s.Cluster},
		{"workspace", s.Workspace},
		{"team", s.Team},
	} {
		if err := checkSegment(seg.name, seg.value); err != nil {
			return "", err
		}
	}
	slot, err := Slot(s.Role, s.Index)
	if err != nil {
		return "", err
	}
	return Scheme + strings.Join([]string{s.Cluster, s.Workspace, s.Team, slot}, "/"), nil
}

// Slot returns <role>-<idx> after checking the slot rules.
func Slot(role string, index int) (string, error) {
	if err := checkSegment("role", role); err != nil {
		return "", err
	}
	if generationTail.MatchString(role) {
		return "", fmt.Errorf("seaturi: role %q ends in -g<digits>, the old generation-bearing shape (CAND-ID-3)", role)
	}
	if index < 0 {
		return "", fmt.Errorf("seaturi: index %d is negative (CAND-ID-3)", index)
	}
	return role + "-" + strconv.Itoa(index), nil
}

// Parse reads a seat URI back into its parts. The raw string is validated,
// never a decoded form (CAND-ID-2).
func Parse(uri string) (Seat, error) {
	rest, ok := strings.CutPrefix(uri, Scheme)
	if !ok {
		return Seat{}, fmt.Errorf("seaturi: %q does not start with the scheme %s", uri, Scheme)
	}
	if strings.ContainsAny(rest, "?#") {
		return Seat{}, fmt.Errorf("seaturi: %q carries a query or fragment (CAND-ID-2)", uri)
	}
	if strings.HasSuffix(rest, "/") {
		return Seat{}, fmt.Errorf("seaturi: %q ends in a slash (CAND-ID-2)", uri)
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 5 {
		return Seat{}, fmt.Errorf("seaturi: %q has 5 segments: the fleet segment was dropped, a seat is %s<cluster>/<ws>/<team>/<slot> (CAND-ID-4)", uri, Scheme)
	}
	if len(parts) != 4 {
		return Seat{}, fmt.Errorf("seaturi: %q has %d segments, want exactly 4 (CAND-ID-4)", uri, len(parts))
	}
	for i, name := range []string{"cluster", "workspace", "team", "slot"} {
		if err := checkSegment(name, parts[i]); err != nil {
			return Seat{}, err
		}
	}
	slot := parts[3]
	cut := strings.LastIndex(slot, "-")
	if cut < 0 {
		return Seat{}, fmt.Errorf("seaturi: slot %q has no -<index> (CAND-ID-3)", slot)
	}
	digits := slot[cut+1:]
	if !indexDigits.MatchString(digits) {
		return Seat{}, fmt.Errorf("seaturi: slot %q: the index %q is not decimal digits without a leading zero (CAND-ID-3)", slot, digits)
	}
	idx, err := strconv.Atoi(digits)
	if err != nil {
		return Seat{}, fmt.Errorf("seaturi: slot %q: index %q out of range (CAND-ID-3)", slot, digits)
	}
	if _, err := Slot(slot[:cut], idx); err != nil {
		return Seat{}, err
	}
	return Seat{
		Cluster: parts[0], Workspace: parts[1], Team: parts[2],
		Role: slot[:cut], Index: idx,
	}, nil
}

var (
	segmentClass   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	generationTail = regexp.MustCompile(`-g[0-9]+$`)
	indexDigits    = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
)

func checkSegment(name, value string) error {
	if !segmentClass.MatchString(value) {
		return fmt.Errorf("seaturi: %s %q is outside [a-z0-9]+(-[a-z0-9]+)* (CAND-ID-1)", name, value)
	}
	return nil
}
