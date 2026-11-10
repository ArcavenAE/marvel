package paths

import (
	"strings"
	"testing"
)

// The daemon serves every connection on its listener as the local admin, so a
// TCP address must be refused wherever a listen address is accepted. The error
// names what to use instead.
func TestRefuseTCPListen(t *testing.T) {
	t.Parallel()
	refused := []string{"127.0.0.1:0", "0.0.0.0:9090", ":9090", "localhost:7000", "tcp://127.0.0.1:9090"}
	for _, addr := range refused {
		err := RefuseTCPListen(addr)
		if err == nil {
			t.Errorf("RefuseTCPListen(%q) = nil, want a refusal", addr)
			continue
		}
		for _, want := range []string{"unix socket", "--mrvl"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("RefuseTCPListen(%q) = %q, want it to mention %q", addr, err, want)
			}
		}
	}
	// Control: a unix socket path, long or short, is not this check's business.
	for _, addr := range []string{"/Users/someone/.marvel/run/marvel.sock", "/tmp/m.sock", strings.Repeat("a", 200)} {
		if err := RefuseTCPListen(addr); err != nil {
			t.Errorf("RefuseTCPListen(%q) = %v, want nil for a unix socket path", addr, err)
		}
	}
}
