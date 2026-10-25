package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandHandoffPath fills a declared handoff template for one session:
// {session} is the session name the seat sees as MARVEL_SESSION, and a leading
// ~/ is the daemon user's home. It is the one expansion both the watcher and
// the seat's MARVEL_HANDOFF_PATH use, so a seat is told exactly the file
// marvel reads.
//
// The template is checked for a .. element at apply, but {session} is filled
// here from the session name, which carries the team and role names. A name
// that is not exactly one path element is refused, so it cannot steer the
// resolved path out of the directory the manifest declared (marvel#444).
func ExpandHandoffPath(template, sessionName string) (string, error) {
	if strings.Contains(template, "{session}") {
		if n := sessionName; n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
			return "", fmt.Errorf("session name %q is not one path element", n)
		}
	}
	p := strings.ReplaceAll(template, "{session}", sessionName)
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~/ in handoff path: %w", err)
		}
		p = filepath.Join(home, rest)
	}
	return p, nil
}
