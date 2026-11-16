package config

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// bus.binary (aae-orc-n5a4y): an absolute nats-server path the supervisor
// execs instead of looking nats-server up on PATH. Unset keeps today's lookup.

// The key parses from a bus: block, resolves unchanged, and is carried by
// Resolve; unset resolves to empty, the PATH lookup.
func TestBusBinaryParsesAndResolves(t *testing.T) {
	t.Parallel()
	none := (&Bus{Managed: true, Listen: "127.0.0.1:4222"}).Resolve("/state")
	if none.Binary != "" {
		t.Errorf("unset binary resolved to %q, want empty", none.Binary)
	}
	if err := ValidateBus("c1", &Bus{Managed: true, Listen: "127.0.0.1:4222"}); err != nil {
		t.Fatalf("an unset binary was refused: %v", err)
	}

	const doc = "managed: true\nlisten: 127.0.0.1:4222\nbinary: /opt/nats/2.2.2/bin/nats-server\n"
	var b Bus
	if err := yaml.Unmarshal([]byte(doc), &b); err != nil {
		t.Fatal(err)
	}
	if b.Binary != "/opt/nats/2.2.2/bin/nats-server" {
		t.Fatalf("parsed binary = %q", b.Binary)
	}
	if err := ValidateBus("c1", &b); err != nil {
		t.Fatalf("an absolute binary was refused: %v", err)
	}
	if got := b.Resolve("/state").Binary; got != "/opt/nats/2.2.2/bin/nats-server" {
		t.Errorf("Resolve().Binary = %q, want the configured path", got)
	}
}

// A relative path, a ~-relative path and an empty-looking one are refused,
// naming the key and the value, so the broker's binary never depends on the
// daemon's working directory or HOME.
func TestValidateBusRefusesARelativeBinary(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"nats-server", "./nats-server", "bin/nats-server", "../nats-server", "~/bin/nats-server", " /opt/x/nats-server"} {
		err := ValidateBus("c1", &Bus{Managed: true, Listen: "127.0.0.1:4222", Binary: bad})
		if !errors.Is(err, ErrInvalidBus) {
			t.Errorf("binary %q: ValidateBus = %v, want ErrInvalidBus", bad, err)
			continue
		}
		if !strings.Contains(err.Error(), "binary") || !strings.Contains(err.Error(), bad) {
			t.Errorf("binary %q: error %q does not name the key and the value", bad, err)
		}
	}
}

// A shim directory is refused: the version manager's answer depends on the
// working directory of the process that execs it.
func TestValidateBusRefusesAShimDirectoryBinary(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"/home/u/.local/share/mise/shims/nats-server",
		"/opt/asdf/shims/nats-server",
		"/a/shims/b/nats-server",
	} {
		err := ValidateBus("c1", &Bus{Managed: true, Listen: "127.0.0.1:4222", Binary: bad})
		if !errors.Is(err, ErrInvalidBus) {
			t.Errorf("binary %q: ValidateBus = %v, want ErrInvalidBus", bad, err)
			continue
		}
		if !strings.Contains(err.Error(), "shim") {
			t.Errorf("binary %q: error %q does not say why", bad, err)
		}
	}
	// A directory that only contains the letters is not a shim directory.
	for _, ok := range []string{"/opt/shimmer/bin/nats-server", "/opt/homebrew/Cellar/nats-server/2.14.6/bin/nats-server"} {
		if err := ValidateBus("c1", &Bus{Managed: true, Listen: "127.0.0.1:4222", Binary: ok}); err != nil {
			t.Errorf("binary %q refused: %v", ok, err)
		}
	}
}

// Only a managed bus runs a binary marvel chooses; on an adopted or external
// bus the key is refused, as the store caps are.
func TestValidateBusRefusesABinaryOnANonManagedBus(t *testing.T) {
	t.Parallel()
	err := ValidateBus("c1", &Bus{URL: "nats://127.0.0.1:4222", Binary: "/opt/nats/2.2.2/bin/nats-server"})
	if !errors.Is(err, ErrInvalidBus) {
		t.Fatalf("ValidateBus = %v, want ErrInvalidBus", err)
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("error %q does not name the key", err)
	}
}

// CheckBusBinary is the one syntactic check the validator and the supervisor
// share.
func TestCheckBusBinary(t *testing.T) {
	t.Parallel()
	if err := CheckBusBinary("/opt/nats/2.2.2/bin/nats-server"); err != nil {
		t.Errorf("a plain absolute path was refused: %v", err)
	}
	for _, bad := range []string{"", "nats-server", "./x", "~/x", "/a/shims/nats-server"} {
		if err := CheckBusBinary(bad); err == nil {
			t.Errorf("CheckBusBinary(%q) accepted", bad)
		}
	}
}
