package bus

import "context"

// WriteFailure is one declared object whose canary write failed.
type WriteFailure struct {
	Object string `json:"object"`
	Err    string `json:"err"`
}

// WriteReading is one write-probe reading of a managed broker.
type WriteReading struct {
	Failures []WriteFailure `json:"failures,omitempty"`
}

// Problems renders the failing set, and doubles as the change key.
func (w WriteReading) Problems() string { return "" }

// ProbeWrites is the stub the red commit compiles against.
func ProbeWrites(ctx context.Context, url, user, password string) (WriteReading, error) {
	return WriteReading{}, nil
}
