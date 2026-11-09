package main

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// attachResolved is what resolveAttachArg found: the desk digits that go on to
// planAttach and the key of the row they belong to.
type attachResolved struct {
	Desk string
	Key  string
}

// panePattern is the pane id a session row carries when it has a desk: a percent
// sign and digits, as tmux prints it.
var panePattern = regexp.MustCompile(`^%([0-9]+)$`)

// resolveAttachArg turns what was typed into a desk, against the rows of the one
// cluster the verb was pointed at. It reads no state, so a row that is not
// running still resolves. The forms, in the order they are told apart:
//
//   - digits, with or without a leading percent sign: a desk. Bare digits that
//     are also the name of a session are refused as ambiguous, because a name may
//     be all digits; %23 is always the desk and aae/23 is always the session;
//   - anything with a slash: a session key, workspace/name, matched exactly;
//   - anything else: a session name, matched exactly and resolved only when one
//     row has it.
//
// Digits that match no desk are an error and never fall back to a name. Only the
// digits of a pane id leave this function, so nothing typed reaches a command
// line.
func resolveAttachArg(arg string, rows []api.Session) (attachResolved, error) {
	if strings.TrimSpace(arg) == "" || arg == "/" {
		return attachResolved{}, errors.New("attach needs a desk number, a session key or a session name")
	}
	if m := deskArg.FindStringSubmatch(arg); m != nil {
		return resolveDeskArg(arg, m[1], rows)
	}
	if strings.Contains(arg, "/") {
		return resolveKeyArg(arg, rows)
	}
	return resolveNameArg(arg, rows)
}

func resolveDeskArg(arg, desk string, rows []api.Session) (attachResolved, error) {
	var onDesk []api.Session
	for _, s := range rows {
		if strings.TrimPrefix(s.PaneID, "%") == desk {
			onDesk = append(onDesk, s)
		}
	}
	if len(onDesk) > 1 {
		return attachResolved{}, fmt.Errorf("desk %s names more than one session row on this cluster", desk)
	}
	if len(onDesk) == 1 && !strings.HasPrefix(arg, "%") {
		if named := rowsNamed(rows, arg); len(named) > 0 {
			return attachResolved{}, collisionError(arg, desk, onDesk[0], named)
		}
	}
	if len(onDesk) == 0 {
		return attachResolved{}, fmt.Errorf("no session has desk %s on this cluster; if you meant a session, pass its key (workspace/name) from `marvel get sessions`", desk)
	}
	return attachResolved{Desk: desk, Key: onDesk[0].Key()}, nil
}

func resolveKeyArg(arg string, rows []api.Session) (attachResolved, error) {
	var found []api.Session
	for _, s := range rows {
		if s.Key() == arg {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return attachResolved{}, fmt.Errorf("no session has key %q on this cluster", arg)
	case 1:
		return resolveRow(found[0])
	}
	return attachResolved{}, fmt.Errorf("key %q names more than one session row on this cluster", arg)
}

func resolveNameArg(arg string, rows []api.Session) (attachResolved, error) {
	found := rowsNamed(rows, arg)
	switch len(found) {
	case 0:
		return attachResolved{}, fmt.Errorf("no session is named %q on this cluster; pass workspace/name, or a desk from `marvel get sessions`", arg)
	case 1:
		return resolveRow(found[0])
	}
	parts := make([]string, len(found))
	for i, s := range found {
		parts[i] = s.Key() + " (" + deskPhrase(s) + ")"
	}
	return attachResolved{}, fmt.Errorf("%q names %d sessions on this cluster; pass one of: %s", arg, len(found), strings.Join(parts, ", "))
}

// resolveRow is the last check for a row found by key or name: it must have a
// pane id that is a percent sign and digits, or there is nothing to attach to.
func resolveRow(s api.Session) (attachResolved, error) {
	m := panePattern.FindStringSubmatch(s.PaneID)
	if m == nil {
		return attachResolved{}, fmt.Errorf("%s has no desk (its pane id is %q): there is no pane to attach to", s.Key(), s.PaneID)
	}
	return attachResolved{Desk: m[1], Key: s.Key()}, nil
}

// rowsNamed returns the rows with this exact name, sorted by key so the
// order of a list never depends on the order the daemon sent.
func rowsNamed(rows []api.Session, name string) []api.Session {
	var found []api.Session
	for _, s := range rows {
		if s.Name == name {
			found = append(found, s)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Key() < found[j].Key() })
	return found
}

// deskPhrase says which desk a row is on, or that it has none.
func deskPhrase(s api.Session) string {
	if m := panePattern.FindStringSubmatch(s.PaneID); m != nil {
		return "desk " + m[1]
	}
	return "no desk"
}

// collisionError refuses digits that are both a desk and the name of a session,
// and says how to ask for each.
func collisionError(arg, desk string, onDesk api.Session, named []api.Session) error {
	if len(named) == 1 {
		return fmt.Errorf("%q is both desk %s (%s) and a session named %s (%s, %s) on this cluster; pass %%%s for the desk or %s for the session",
			arg, desk, onDesk.Key(), arg, named[0].Key(), deskPhrase(named[0]), desk, named[0].Key())
	}
	parts := make([]string, len(named))
	for i, s := range named {
		parts[i] = s.Key() + ", " + deskPhrase(s)
	}
	return fmt.Errorf("%q is both desk %s (%s) and the name of %d sessions (%s) on this cluster; pass %%%s for the desk or a key such as %s for a session",
		arg, desk, onDesk.Key(), len(named), strings.Join(parts, "; "), desk, named[0].Key())
}
