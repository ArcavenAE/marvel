package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// versionProbeBinary writes a stand-in nats-server that records the
// environment it was started with in a file and answers --version. The record
// path is written into the script, because the child is not given any
// variable to carry it.
func versionProbeBinary(t *testing.T) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "env.txt")
	bin = filepath.Join(dir, "nats-server")
	script := "#!/bin/sh\nenv > '" + record + "'\necho 'nats-server: v2.14.6'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

func TestTheVersionProbeChildDoesNotInheritTheDaemonEnvironment(t *testing.T) {
	bin, record := versionProbeBinary(t)
	t.Setenv("MARVEL_TEST_PROBE_CANARY", "canary-value")
	t.Setenv(LeafSeedEnv, "seed-canary-value")

	if got := binaryVersion(bin); got != "v2.14.6" {
		t.Fatalf("binaryVersion = %q, want v2.14.6", got)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the probe child recorded no environment: %v", err)
	}
	seen := string(data)
	for _, leaked := range []string{"MARVEL_TEST_PROBE_CANARY", LeafSeedEnv, "canary-value"} {
		if strings.Contains(seen, leaked) {
			t.Errorf("the probe child inherited %q from the daemon environment", leaked)
		}
	}
}

func TestTheVersionProbeChildKeepsTheClassAllowlist(t *testing.T) {
	bin, record := versionProbeBinary(t)
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("HOME", "/probe-home")
	t.Setenv("TMPDIR", "/probe-tmp")

	binaryVersion(bin)
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the probe child recorded no environment: %v", err)
	}
	for _, want := range []string{"HOME=/probe-home", "TMPDIR=/probe-tmp", "PATH="} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the probe child lacks %q from the class allowlist", want)
		}
	}
}
