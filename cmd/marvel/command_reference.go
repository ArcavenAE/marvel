package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// normalizeHome writes the running user's home directory as "~". Some flag
// defaults are computed from the home directory (the daemon socket, log and
// pidfile paths), so the raw text would carry the generating machine's path,
// or a test's random scratch home, and the committed reference would differ on
// every host. The symlink-resolved form is replaced too, since a temp
// directory can be reached by two spellings on macOS.
func normalizeHome(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 2 {
		return s
	}
	forms := []string{home}
	if real, err := filepath.EvalSymlinks(home); err == nil && real != home {
		forms = append(forms, real)
	}
	sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	for _, f := range forms {
		s = strings.ReplaceAll(s, f, "~")
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
