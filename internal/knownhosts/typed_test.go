package knownhosts

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestUnknownHostIsTypedInEveryRefusingMode: both modes that refuse an
// unknown host, strict and prompt with no terminal, return the typed error
// with the offered key's fingerprint.
func TestUnknownHostIsTypedInEveryRefusingMode(t *testing.T) {
	for _, m := range []struct {
		name string
		mode Mode
	}{{"strict", ModeStrict}, {"prompt off a terminal", ModePrompt}} {
		t.Run(m.name, func(t *testing.T) {
			cb := Callback(testLayout(t), m.mode, &bytes.Buffer{}, strings.NewReader(""))
			key := genKey(t)
			err := cb("127.0.0.1:6785", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 6785}, key)
			var unk *HostKeyUnknownError
			if !errors.As(err, &unk) {
				t.Fatalf("err = %v (%T), want *HostKeyUnknownError", err, err)
			}
			if unk.Fingerprint != ssh.FingerprintSHA256(key) || unk.Host != "127.0.0.1:6785" {
				t.Errorf("Host, Fingerprint = %q, %q; want 127.0.0.1:6785, %q", unk.Host, unk.Fingerprint, ssh.FingerprintSHA256(key))
			}
		})
	}
}
