package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arcavenae/marvel/internal/paths"
	"github.com/spf13/cobra"
)

// commandReference renders one markdown page per visible command of the cobra
// tree rooted at root. The key is the page file name, the value is its
// content. The output depends on the tree alone: no date, no host, no
// generator stamp. docs/reference holds the committed copy, and
// TestCommittedCommandReferenceMatchesTheTree fails when the two differ.
//
// The page layout follows cobra's own markdown generator (title, synopsis,
// usage, examples, options, inherited options, see also) without its
// auto-generation footer. cobra/doc is not used because it pulls two modules
// into this module's go.mod for a writer this small.
func commandReference(root *cobra.Command) map[string]string {
	pages := map[string]string{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		pages[referenceFile(c)] = referencePage(c)
		for _, sub := range referenceChildren(c) {
			walk(sub)
		}
	}
	walk(root)
	for name, page := range pages {
		pages[name] = normalizeHome(page)
	}
	return pages
}

// referenceHome is the marvel home the reference is generated under. It is
// short, so its socket path is inside the 104-byte limit, and it is replaced
// with "~/.marvel" in the pages. Flag defaults read $MARVEL_HOME when the tree
// is built, and a long home (a temp dir under a long TMPDIR, a long HOME) makes
// the daemon drop its path defaults, so the tree is built under this one
// whatever the generating process has set.
const referenceHome = "/marvel-reference-home"

// newReferenceRoot builds the command tree the reference is generated from,
// with $MARVEL_HOME pinned to referenceHome while the flags are defined.
func newReferenceRoot() *cobra.Command {
	prev, had := os.LookupEnv(paths.HomeEnv)
	_ = os.Setenv(paths.HomeEnv, referenceHome)
	defer func() {
		if had {
			_ = os.Setenv(paths.HomeEnv, prev)
		} else {
			_ = os.Unsetenv(paths.HomeEnv)
		}
	}()
	return newRootCmd()
}

// normalizeHome writes the generating process's marvel home as "~/.marvel" and
// the running user's home directory as "~". Some flag defaults are computed
// from the marvel home (the daemon socket, log and pidfile paths), which is
// $MARVEL_HOME when that is set and $HOME/.marvel otherwise, so the raw text
// would carry the generating machine's path, or a test's random scratch home,
// and the committed reference would differ on every host. The marvel home is
// replaced first, since it can sit outside the user's home. The
// symlink-resolved form of each is replaced too, since a temp directory can be
// reached by two spellings on macOS.
func normalizeHome(s string) string {
	s = replaceRoot(s, referenceHome, "~/.marvel")
	if layout, err := paths.Default(); err == nil {
		s = replaceRoot(s, layout.Home, "~/.marvel")
	}
	if home, err := os.UserHomeDir(); err == nil {
		s = replaceRoot(s, home, "~")
	}
	return s
}

// replaceRoot replaces root, and its symlink-resolved spelling, with to,
// longest spelling first. A root shorter than two bytes is left alone, so an
// empty path or "/" cannot rewrite the whole text.
func replaceRoot(s, root, to string) string {
	if len(root) < 2 {
		return s
	}
	forms := []string{root}
	if real, err := filepath.EvalSymlinks(root); err == nil && real != root {
		forms = append(forms, real)
	}
	sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	for _, f := range forms {
		s = strings.ReplaceAll(s, f, to)
	}
	return s
}

// referenceFile is the page file name for a command: its path with spaces as
// underscores, so `marvel get` is marvel_get.md.
func referenceFile(c *cobra.Command) string {
	return strings.ReplaceAll(c.CommandPath(), " ", "_") + ".md"
}

// referenceChildren returns the commands a user can see under c, sorted by
// name so the output does not depend on registration order.
func referenceChildren(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Deprecated != "" {
			continue
		}
		out = append(out, sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

func referencePage(c *cobra.Command) string {
	var b strings.Builder
	path := c.CommandPath()
	fmt.Fprintf(&b, "## %s\n\n", path)
	if c.Short != "" {
		fmt.Fprintf(&b, "%s\n\n", c.Short)
	}

	long := strings.TrimSpace(c.Long)
	if long == "" {
		long = strings.TrimSpace(c.Short)
	}
	if long != "" {
		fmt.Fprintf(&b, "### Synopsis\n\n%s\n\n", long)
	}
	if c.Runnable() {
		fmt.Fprintf(&b, "```\n%s\n```\n\n", c.UseLine())
	}
	if ex := strings.TrimSpace(c.Example); ex != "" {
		fmt.Fprintf(&b, "### Examples\n\n```\n%s\n```\n\n", ex)
	}
	if usage := strings.TrimRight(c.NonInheritedFlags().FlagUsages(), "\n"); usage != "" {
		fmt.Fprintf(&b, "### Options\n\n```\n%s\n```\n\n", usage)
	}
	if usage := strings.TrimRight(c.InheritedFlags().FlagUsages(), "\n"); usage != "" {
		fmt.Fprintf(&b, "### Options inherited from parent commands\n\n```\n%s\n```\n\n", usage)
	}

	children := referenceChildren(c)
	if c.HasParent() || len(children) > 0 {
		b.WriteString("### SEE ALSO\n\n")
		if c.HasParent() {
			fmt.Fprintf(&b, "* [%s](%s)\t - %s\n", c.Parent().CommandPath(), referenceFile(c.Parent()), c.Parent().Short)
		}
		for _, sub := range children {
			fmt.Fprintf(&b, "* [%s](%s)\t - %s\n", sub.CommandPath(), referenceFile(sub), sub.Short)
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
