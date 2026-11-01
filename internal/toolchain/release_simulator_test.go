package toolchain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The release ships marvel-simulator next to marvel, so a newcomer's first
// tour needs one install and no Go toolchain (marvel#1175). These tests hold
// that for each channel: the alpha pipeline in ci.yml, the stable pipeline in
// .goreleaser.yml, a source build, and the manifests that name the program.

var releasePlatforms = []string{"darwin-arm64", "darwin-amd64", "linux-amd64", "linux-arm64"}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Given a release, each marvel binary has a matching simulator binary. The
// alpha release uploads every marvel-* file and checksums and attests them all,
// so the build step must produce both names for each platform.
func TestAlphaBuildProducesASimulatorForEachMarvelBinary(t *testing.T) {
	t.Parallel()
	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var run string
	for _, s := range jobs["build-binaries"].Steps {
		if s.Name == "Build alpha binaries" {
			run = s.Run
		}
	}
	if run == "" {
		t.Fatal("build-binaries has no step named \"Build alpha binaries\"")
	}
	for _, p := range releasePlatforms {
		for _, want := range []string{
			"-o marvel-" + p + " ./cmd/marvel",
			"-o marvel-simulator-" + p + " ./cmd/simulator",
		} {
			spaced := strings.ReplaceAll(regexp.QuoteMeta(want), " ", `\s+`)
			if !regexp.MustCompile(`go build[^\n]*` + spaced).MatchString(run) {
				t.Errorf("the alpha build has no `go build ... %s`", want)
			}
		}
	}
	// The simulator reports the version marvel version compares it with.
	if !strings.Contains(run, "-X main.version=") || strings.Count(run, "$LDFLAGS") < 8 {
		t.Errorf("the alpha build must stamp all eight binaries with the same LDFLAGS")
	}
}

// With signing on, the release job takes the darwin marvel binaries from the
// signed artifact, and nothing else from it. The simulators come from the
// unsigned build artifact, all four, or they never reach the release.
func TestSignedReleaseStillCarriesEverySimulator(t *testing.T) {
	t.Parallel()
	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var run string
	for _, s := range jobs["release"].Steps {
		if strings.Contains(s.Run, "unsigned-binaries/") {
			run += s.Run + "\n"
		}
	}
	for _, p := range releasePlatforms {
		if !strings.Contains(run, "unsigned-binaries/marvel-simulator-"+p) {
			t.Errorf("the release job does not copy marvel-simulator-%s out of unsigned-binaries", p)
		}
	}
}

type goreleaserBuild struct {
	ID      string   `yaml:"id"`
	Main    string   `yaml:"main"`
	Binary  string   `yaml:"binary"`
	Goos    []string `yaml:"goos"`
	Goarch  []string `yaml:"goarch"`
	Ldflags []string `yaml:"ldflags"`
}

// The stable pipeline builds the simulator for the same platforms as marvel,
// stamps it the same way, and the formulas install both.
func TestStableReleaseBuildsAndInstallsTheSimulator(t *testing.T) {
	t.Parallel()
	var cfg struct {
		Builds []goreleaserBuild `yaml:"builds"`
		Brews  []struct {
			Name    string `yaml:"name"`
			Install string `yaml:"install"`
		} `yaml:"brews"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, ".goreleaser.yml")), &cfg); err != nil {
		t.Fatal(err)
	}
	var m, sim *goreleaserBuild
	for i := range cfg.Builds {
		switch cfg.Builds[i].Binary {
		case "marvel":
			m = &cfg.Builds[i]
		case "marvel-simulator":
			sim = &cfg.Builds[i]
		}
	}
	if m == nil || sim == nil {
		t.Fatalf("builds: marvel=%v marvel-simulator=%v, want both", m != nil, sim != nil)
	}
	if sim.Main != "./cmd/simulator" {
		t.Errorf("the simulator build's main = %q, want ./cmd/simulator", sim.Main)
	}
	if strings.Join(sim.Goos, ",") != strings.Join(m.Goos, ",") || strings.Join(sim.Goarch, ",") != strings.Join(m.Goarch, ",") {
		t.Errorf("the simulator builds for %v/%v, marvel for %v/%v; they must match", sim.Goos, sim.Goarch, m.Goos, m.Goarch)
	}
	for _, want := range []string{"-X main.version={{.Version}}", "-X main.channel=stable"} {
		if !slicesContains(sim.Ldflags, want) {
			t.Errorf("the simulator build lacks ldflag %q", want)
		}
	}
	if len(cfg.Brews) == 0 {
		t.Fatal("no brews entries")
	}
	for _, b := range cfg.Brews {
		for _, bin := range []string{`bin.install "marvel"`, `bin.install "marvel-simulator"`} {
			if !strings.Contains(b.Install, bin) {
				t.Errorf("brews entry %q does not run %s", b.Name, bin)
			}
		}
	}
}

func slicesContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// just build writes the simulator under the name a release carries, so a
// source build and an install behave alike once bin is on PATH.
func TestJustBuildWritesTheReleaseNamedSimulator(t *testing.T) {
	t.Parallel()
	text := readRepoFile(t, "justfile")
	build := text[strings.Index(text, "\nbuild:"):]
	if end := strings.Index(build[1:], "\n\n"); end > 0 {
		build = build[:end+1]
	}
	if !strings.Contains(build, "-o bin/marvel-simulator ./cmd/simulator") {
		t.Errorf("just build does not write bin/marvel-simulator:\n%s", build)
	}
}

// The tour names the simulator by the name the release installs, so it runs
// from an install with no source tree.
func TestTourManifestsNameTheInstalledSimulator(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"tour.toml", "tour.yaml"} {
		text := readRepoFile(t, "examples", f)
		if strings.Contains(text, "bin/simulator") {
			t.Errorf("examples/%s still points at a source-tree path, bin/simulator", f)
		}
		if !regexp.MustCompile(`command\s*[=:]\s*"?marvel-simulator"?\s*\n`).MatchString(text) {
			t.Errorf("examples/%s does not use command marvel-simulator", f)
		}
	}
}
