package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The release notes tell a reader which parts of this release are unfinished.
// Five surfaces sit behind --experimental: the --mode, --prompt-file, --env and
// --name flags of `marvel run`, and `marvel wait` (cmd/marvel/main.go and
// wait.go). A reader who only sees the release page should learn that from its
// header, not from a help line, so the header names each one.

func releaseHeader(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Release struct {
			Header string `yaml:"header"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yml: %v", err)
	}
	return cfg.Release.Header
}

func TestReleaseHeaderListsTheExperimentalSurfacesAsUnfinished(t *testing.T) {
	header := releaseHeader(t)
	for _, want := range []string{"unfinished", "--experimental", "marvel run", "--mode", "--prompt-file", "--env", "--name", "marvel wait"} {
		if !strings.Contains(header, want) {
			t.Errorf("release header does not mention %q:\n%s", want, header)
		}
	}
}

// The header still opens with the version and the one-line description it had.
func TestReleaseHeaderKeepsItsOpening(t *testing.T) {
	header := releaseHeader(t)
	if !strings.HasPrefix(header, "## marvel v{{.Version}}\n\nAgent orchestration control plane.") {
		t.Errorf("release header opening changed:\n%s", header)
	}
}
