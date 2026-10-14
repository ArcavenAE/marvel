// Package seaturi is the one place marvel cuts a seat's identity string.
//
// A seat is named marvel://<fleet>/<cluster>/<ws>/<team>/<slot>, where the
// slot is <role>-<idx>. The generation and instance are liveness facts and
// never ride in the name, so a shift keeps the URI. Every identifier marvel
// stamps for a seat is cut from Build's output; a string assembled by hand
// elsewhere is a defect (aae-orc docs/design/seat-identity-grammar.md,
// section 3 and CAND-ID-6).
package seaturi

import "errors"

// Scheme is the URI prefix of a seat identity.
const Scheme = "marvel://"

// Seat is the five names and the index that identify one slot.
type Seat struct {
	Fleet     string
	Cluster   string
	Workspace string
	Team      string
	Role      string
	Index     int
}

var errNotBuilt = errors.New("seaturi: not implemented")

// Build returns the seat URI, or an error naming the first rule the seat
// breaks. A string that breaks a rule is rejected, never rewritten.
func Build(s Seat) (string, error) { return "", errNotBuilt }

// Slot returns <role>-<idx> after checking the slot rules.
func Slot(role string, index int) (string, error) { return "", errNotBuilt }

// Parse reads a seat URI back into its parts, validating the raw string.
func Parse(uri string) (Seat, error) { return Seat{}, errNotBuilt }
