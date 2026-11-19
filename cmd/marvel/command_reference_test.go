package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

var updateReference = flag.Bool("update-reference", false,
	"rewrite docs/reference from the cobra tree instead of comparing against it")

const referenceDir = "../../docs/reference"

// referencePageName is the page file for a command: its path with spaces as
// underscores, so `marvel get` is marvel_get.md.
func referencePageName(c *cobra.Command) string {
	return strings.ReplaceAll(c.CommandPath(), " ", "_") + ".md"
}

// visibleCommands lists the root and every command a user can see, in tree
// order.
func visibleCommands(c *cobra.Command) []*cobra.Command {
	out := []*cobra.Command{c}
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Deprecated != "" {
			continue
		}
		out = append(out, visibleCommands(sub)...)
	}
	return out
}

func TestCommandReferenceHasAPagePerVisibleCommand(t *testing.T) {
	root := newRootCmd()
	pages := commandReference(root)
	if len(pages) == 0 {
		t.Fatal("commandReference returned no pages for the marvel command tree")
	}
	for _, c := range visibleCommands(root) {
		name := referencePageName(c)
		page, ok := pages[name]
		if !ok {
			t.Errorf("no page %s for the command %q", name, c.CommandPath())
			continue
		}
		if !strings.Contains(page, c.CommandPath()) {
			t.Errorf("page %s does not name its command %q", name, c.CommandPath())
		}
		if c.Short != "" && !strings.Contains(page, c.Short) {
			t.Errorf("page %s omits the short description %q", name, c.Short)
		}
	}
}

func TestCommandReferencePagesNameEveryLocalFlag(t *testing.T) {
	root := newRootCmd()
	pages := commandReference(root)
	if len(pages) == 0 {
		t.Fatal("commandReference returned no pages for the marvel command tree")
	}
	for _, c := range visibleCommands(root) {
		page := pages[referencePageName(c)]
		for _, name := range localFlagNames(c) {
			if !strings.Contains(page, "--"+name) {
				t.Errorf("page for %q omits the flag --%s", c.CommandPath(), name)
			}
		}
	}
}

var flagUsageName = regexp.MustCompile(`^\s+(?:-\w, )?--([A-Za-z0-9-]+)`)

// localFlagNames lists the visible local flags of c from cobra's own usage
// text, which leaves hidden flags out. It reads names from the text so the
// test needs no import of pflag, an indirect dependency of this module.
func localFlagNames(c *cobra.Command) []string {
	var names []string
	for _, line := range strings.Split(c.LocalFlags().FlagUsages(), "\n") {
		if m := flagUsageName.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names
}

func TestCommandReferenceIsDeterministic(t *testing.T) {
	first := commandReference(newRootCmd())
	if len(first) == 0 {
		t.Fatal("commandReference returned no pages for the marvel command tree")
	}
	second := commandReference(newRootCmd())
	if len(first) != len(second) {
		t.Fatalf("two runs produced %d and %d pages", len(first), len(second))
	}
	for name, page := range first {
		if second[name] != page {
			t.Errorf("page %s differs between two runs on the same tree", name)
		}
	}
}

func TestCommandReferenceCarriesNoGenerationStamp(t *testing.T) {
	pages := commandReference(newRootCmd())
	if len(pages) == 0 {
		t.Fatal("commandReference returned no pages for the marvel command tree")
	}
	for name, page := range pages {
		if strings.Contains(page, "Auto generated") {
			t.Errorf("page %s carries an auto-generation stamp, which changes the output", name)
		}
	}
}

// TestCommittedCommandReferenceMatchesTheTree fails when docs/reference
// differs from what the cobra tree generates. Regenerate with:
//
//	go test ./cmd/marvel -run TestCommittedCommandReferenceMatchesTheTree -update-reference
func TestCommittedCommandReferenceMatchesTheTree(t *testing.T) {
	generated := commandReference(newRootCmd())
	if len(generated) == 0 {
		t.Fatal("commandReference returned no pages for the marvel command tree")
	}
	names := make([]string, 0, len(generated))
	for name := range generated {
		names = append(names, name)
	}
	sort.Strings(names)

	if *updateReference {
		if err := os.MkdirAll(referenceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(referenceDir, name), []byte(generated[name]), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	committed, err := filepath.Glob(filepath.Join(referenceDir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, path := range committed {
		name := filepath.Base(path)
		seen[name] = true
		want, ok := generated[name]
		if !ok {
			t.Errorf("docs/reference/%s has no command in the tree; delete the file", name)
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("docs/reference/%s differs from the generated page; run: go test ./cmd/marvel -run TestCommittedCommandReferenceMatchesTheTree -update-reference", name)
		}
	}
	for _, name := range names {
		if !seen[name] {
			t.Errorf("docs/reference/%s is missing; run: go test ./cmd/marvel -run TestCommittedCommandReferenceMatchesTheTree -update-reference", name)
		}
	}
}
