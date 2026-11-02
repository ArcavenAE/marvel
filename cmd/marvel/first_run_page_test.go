package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The first-run page promises stages that each work without the ones after
// them and end with one proof command. These checks read the page the way a
// newcomer would run it: every command is a marvel verb that exists (or the
// one package-manager line for a prerequisite), no stage needs a source tree
// or a tool from a later stage, and the manifest and config the page tells the
// user to write load with marvel's own parsers.

const firstRunPage = "../../docs/first-run.md"

var (
	stageHeading = regexp.MustCompile(`(?m)^## Stage ([0-9]):`)
	fencedBlock  = regexp.MustCompile("(?s)```([a-z]*)\n(.*?)```")
	backticked   = regexp.MustCompile("`([^`]+)`")
)

// firstRunStages returns the text of each stage by number.
func firstRunStages(t *testing.T, page string) map[string]string {
	t.Helper()
	idx := stageHeading.FindAllStringSubmatchIndex(page, -1)
	if len(idx) != 4 {
		t.Fatalf("want stages 0 to 3 on the page, found %d stage headings", len(idx))
	}
	stages := map[string]string{}
	for i, m := range idx {
		end := len(page)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		stages[page[m[2]:m[3]]] = page[m[0]:end]
	}
	return stages
}

// marvelVerbExists reports whether "marvel <args...>" names a command the
// real root command has.
func marvelVerbExists(args []string) bool {
	root := newRootCmd()
	var words []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || a == "&" || a == ">" {
			break
		}
		words = append(words, a)
	}
	cmd, _, err := root.Find(words)
	return err == nil && cmd != nil && cmd != root
}

func TestFirstRunPageStages(t *testing.T) {
	raw, err := os.ReadFile(firstRunPage)
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	stages := firstRunStages(t, page)

	// What each stage may not name: a tool that belongs to a later stage.
	later := map[string][]string{
		"0": {"claude", "codex", "opencode", "nats", "director"},
		"1": {"nats", "director"},
		"2": {"director"},
	}
	for _, n := range []string{"0", "1", "2"} {
		text := stages[n]
		if got := strings.Count(text, "\nProof: "); got != 1 {
			t.Errorf("stage %s names %d proof lines, want exactly one", n, got)
		}
		lower := strings.ToLower(text)
		for _, tool := range later[n] {
			if strings.Contains(lower, tool) {
				t.Errorf("stage %s names %q, a tool from a later stage", n, tool)
			}
		}
		var commands []string
		for _, b := range fencedBlock.FindAllStringSubmatch(text, -1) {
			if b[1] != "sh" {
				continue
			}
			for _, line := range strings.Split(b[2], "\n") {
				if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
					commands = append(commands, line)
				}
			}
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "Proof: ") {
				for _, m := range backticked.FindAllStringSubmatch(line, -1) {
					if strings.HasPrefix(m[1], "marvel ") {
						commands = append(commands, m[1])
					}
				}
			}
		}
		if len(commands) == 0 {
			t.Errorf("stage %s has no command", n)
		}
		for _, c := range commands {
			f := strings.Fields(c)
			switch f[0] {
			case "brew":
			case "marvel":
				if len(f) < 2 || !marvelVerbExists(f[1:]) {
					t.Errorf("stage %s runs %q, which is not a marvel command", n, c)
				}
			default:
				t.Errorf("stage %s runs %q; only marvel and the package manager are allowed", n, c)
			}
			for _, bad := range []string{"./", "bin/", "examples/", "go ", "just "} {
				if strings.Contains(c, bad) {
					t.Errorf("stage %s runs %q, which needs a source tree (%q)", n, c, bad)
				}
			}
		}
	}

	// Stage 3 is a placeholder: no command, no proof.
	if strings.Contains(stages["3"], "```") || strings.Contains(stages["3"], "Proof:") {
		t.Error("stage 3 carries a command or a proof before its release exists")
	}

	for _, want := range []string{"Teach auto mode", "2. Not now"} {
		if !strings.Contains(page, want) {
			t.Errorf("the First seat box lacks %q", want)
		}
	}
	for _, bad := range []string{"/Users/", "/home/", "~/.marvel/manifests"} {
		if strings.Contains(page, bad) {
			t.Errorf("the page names %q", bad)
		}
	}
}

// The manifest stage 1 tells the user to save and the config stage 2 tells
// them to write must load with marvel's own parsers.
func TestFirstRunPageSnippetsLoad(t *testing.T) {
	raw, err := os.ReadFile(firstRunPage)
	if err != nil {
		t.Fatal(err)
	}
	stages := firstRunStages(t, string(raw))
	block := func(stage, lang string) string {
		for _, b := range fencedBlock.FindAllStringSubmatch(stages[stage], -1) {
			if b[1] == lang {
				return b[2]
			}
		}
		t.Fatalf("stage %s has no %s block", stage, lang)
		return ""
	}

	m, err := api.ParseManifestBytes([]byte(block("1", "toml")))
	if err != nil {
		t.Fatalf("the stage 1 manifest does not parse: %v", err)
	}
	if len(m.Teams) != 1 || len(m.Teams[0].Roles) != 1 || m.Teams[0].Roles[0].Runtime.Command != "claude" {
		t.Errorf("the stage 1 manifest is not one claude seat: %+v", m.Teams)
	}

	// A short root: the socket path under it must fit a unix socket's limit.
	home, err := os.MkdirTemp("/tmp", "fr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("MARVEL_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(block("2", "yaml")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("the stage 2 config does not load: %v", err)
	}
	if len(cfg.Clusters) != 1 {
		t.Fatalf("the stage 2 config has %d clusters, want 1", len(cfg.Clusters))
	}
	if err := config.ValidateServices(&cfg.Clusters[0]); err != nil {
		t.Errorf("the stage 2 bus entry is refused: %v", err)
	}
	svc, err := cfg.Clusters[0].BusService()
	if err != nil || svc == nil || svc.Mode != "managed" {
		t.Errorf("the stage 2 config does not declare a managed bus: %+v, %v", svc, err)
	}
}
