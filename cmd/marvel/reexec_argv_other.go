//go:build !linux && !darwin

package main

import "errors"

// readProcessArgs has no exact source on this system, so the pre-flight
// refuses rather than guess from a flattened line.
func readProcessArgs(int) ([]string, error) {
	return nil, errors.New("no exact argument source on this system")
}
