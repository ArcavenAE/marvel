package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemon"
)

// The two notes travel with the output so a reader who only has the file knows
// what each section is. The desired section is the store's record after scale
// and similar edits, which is not the file that was applied (comments,
// ordering and unset fields are not kept), so no round trip is promised. The
// observed section is live state and is not an input to marvel work.
const (
	desiredNote  = "the stored desired state after scale and similar edits; not the applied file, and no round trip to it is promised"
	observedNote = "live sessions at read time; not for re-apply"
)

// buildGetOutput is the document `get team|workspace -o` prints: the stored
// desired state and the observed sessions, in two labelled sections
// (aae-orc-zv4jw, operator ruling Q2 (c) of 2026-10-08). Both sections go
// through api.Redact, so a secret-looking Env value is "(redacted)" even when
// the daemon that answered did not redact (an older daemon).
func buildGetOutput(resource string, teams []api.Team, workspaces []api.Workspace, sessions []api.Session) (map[string]any, error) {
	var desired, observed any
	switch strings.TrimSuffix(resource, "s") {
	case "team":
		desired, observed = api.Redact(teams), observedTeams(teams, sessions)
	case "workspace":
		desired, observed = api.Redact(workspaces), observedWorkspaces(workspaces, teams, sessions)
	default:
		return nil, fmt.Errorf("-o applies to team and workspace only, not %q", resource)
	}
	return map[string]any{
		"kind":     strings.TrimSuffix(resource, "s"),
		"desired":  map[string]any{"note": desiredNote, "items": desired},
		"observed": map[string]any{"note": observedNote, "items": observed},
	}, nil
}

type observedSession struct {
	Name       string `json:"name"`
	Generation int64  `json:"generation"`
	State      string `json:"state"`
}

type observedRole struct {
	Role     string            `json:"role"`
	ByState  map[string]int    `json:"by_state"`
	Sessions []observedSession `json:"sessions"`
}

type observedTeam struct {
	Workspace string         `json:"workspace"`
	Team      string         `json:"team"`
	Roles     []observedRole `json:"roles"`
}

func observedTeams(teams []api.Team, sessions []api.Session) []observedTeam {
	out := make([]observedTeam, 0, len(teams))
	for _, t := range teams {
		ot := observedTeam{Workspace: t.Workspace, Team: t.Name, Roles: []observedRole{}}
		for _, r := range t.Roles {
			or := observedRole{Role: r.Name, ByState: map[string]int{}, Sessions: []observedSession{}}
			for _, s := range sessions {
				if s.Workspace != t.Workspace || s.Team != t.Name || s.Role != r.Name {
					continue
				}
				or.ByState[string(s.State)]++
				or.Sessions = append(or.Sessions, observedSession{Name: s.Name, Generation: s.Generation, State: string(s.State)})
			}
			ot.Roles = append(ot.Roles, or)
		}
		out = append(out, ot)
	}
	return out
}

type observedWorkspace struct {
	Workspace string         `json:"workspace"`
	Teams     int            `json:"teams"`
	ByState   map[string]int `json:"by_state"`
}

func observedWorkspaces(workspaces []api.Workspace, teams []api.Team, sessions []api.Session) []observedWorkspace {
	out := make([]observedWorkspace, 0, len(workspaces))
	for _, w := range workspaces {
		ow := observedWorkspace{Workspace: w.Name, ByState: map[string]int{}}
		for _, t := range teams {
			if t.Workspace == w.Name {
				ow.Teams++
			}
		}
		for _, s := range sessions {
			if s.Workspace == w.Name {
				ow.ByState[string(s.State)]++
			}
		}
		out = append(out, ow)
	}
	return out
}

// renderGetOutput encodes the document as json or yaml. Both carry the same
// keys: the yaml is the json document read back and re-encoded.
func renderGetOutput(doc map[string]any, format string) (string, error) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	switch format {
	case "json":
		return string(data) + "\n", nil
	case "yaml":
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return "", err
		}
		y, err := yaml.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(y), nil
	default:
		return "", fmt.Errorf("unknown output format %q: use json or yaml", format)
	}
}

// fetchGet reads one resource type through the daemon into out.
func fetchGet(resourceType string, out any) error {
	params, _ := json.Marshal(map[string]string{"resource_type": resourceType})
	resp, err := send(daemon.Request{Method: "get", Params: params})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return json.Unmarshal(resp.Result, out)
}

// getWithOutput prints `get team|workspace -o json|yaml`.
func getWithOutput(resource, format string) error {
	switch strings.TrimSuffix(resource, "s") {
	case "team", "workspace":
	default:
		return fmt.Errorf("-o applies to team and workspace only, not %q", resource)
	}
	if format != "json" && format != "yaml" {
		return fmt.Errorf("unknown output format %q: use json or yaml", format)
	}
	var teams []api.Team
	var workspaces []api.Workspace
	var sessions []api.Session
	if err := fetchGet("teams", &teams); err != nil {
		return err
	}
	if strings.TrimSuffix(resource, "s") == "workspace" {
		if err := fetchGet("workspaces", &workspaces); err != nil {
			return err
		}
	}
	if err := fetchGet("sessions", &sessions); err != nil {
		return err
	}
	doc, err := buildGetOutput(resource, teams, workspaces, sessions)
	if err != nil {
		return err
	}
	out, err := renderGetOutput(doc, format)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}
