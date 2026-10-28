package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// reportPrune prints what marvel work --prune did, from the apply response
// (docs/design/work-prune.md section 5). A response with no pruned array came
// from a daemon that applied and dropped the prune, so it is exit 1; the apply
// stands.
func reportPrune(w io.Writer, result json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result, &fields); err != nil {
		return &exitError{Code: 1, Msg: "decode the apply response; the apply stood and the prune is unknown", Err: err}
	}
	raw, ok := fields["pruned"]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return &exitError{Code: 1, Msg: "the daemon applied the manifest but reported no prune (it predates marvel work --prune); nothing was pruned"}
	}
	var r struct {
		Pruned   []string            `json:"pruned"`
		Draining []api.PruneDraining `json:"draining"`
		Skipped  []api.PruneSkipped  `json:"prune_skipped"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return &exitError{Code: 1, Msg: "decode the prune result; the apply stood", Err: err}
	}
	var b strings.Builder
	if len(r.Pruned) == 0 {
		fmt.Fprintln(&b, "pruned: nothing")
	}
	for _, k := range r.Pruned {
		fmt.Fprintf(&b, "pruned %s\n", k)
	}
	// A step 3 daemon sends no draining key, which reads as none.
	for _, d := range r.Draining {
		fmt.Fprintf(&b, "draining %s   %s asked for their handoff; the team is deleted once they have left\n", d.Team, plural(d.Seats, "seat"))
	}
	for _, s := range r.Skipped {
		fmt.Fprintf(&b, "not pruned %s   %s\n", s.Team, s.Reason)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
