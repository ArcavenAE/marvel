package main

import (
	"errors"
	"io"
)

// getVitals prints what `marvel get vitals` shows.
func getVitals(w io.Writer, output string) error {
	return errors.New("get vitals is not built")
}
