package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// planPrune asks the daemon's read-only prune_plan for what marvel work --prune
// would delete for the manifest at path. It sends apply's params plus the
// manifest source, never the apply method, so a daemon without prune_plan
// answers "unknown method" and the preview fails closed with exit 1
// (docs/design/work-prune.md section 4).
func planPrune(cmd *cobra.Command, path string, detailed bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return &exitError{Code: 1, Msg: "read manifest", Err: err}
	}
	src, err := manifestSource(path)
	if err != nil {
		return &exitError{Code: 1, Msg: fmt.Sprintf("could not compute manifest source (%v); nothing can be pruned without one", err), Err: err}
	}
	p := map[string]any{"manifest_data": data, "manifest_source": src}
	if root := workspaceRootFor(path, declaredWorkspaceRoot(data)); root != "" {
		p["workspace_root"] = root
	}
	params, err := json.Marshal(p)
	if err != nil {
		return &exitError{Code: 1, Msg: "encode prune plan params", Err: err}
	}
	resp, err := send(daemonrpc.Request{Method: "prune_plan", Params: params})
	if err != nil {
		return &exitError{Code: 1, Msg: "could not evaluate the prune plan", Err: err}
	}
	if resp.Error != "" {
		return &exitError{Code: 1, Msg: "could not evaluate the prune plan: " + resp.Error}
	}
	var plan api.PrunePlan
	if err := json.Unmarshal(resp.Result, &plan); err != nil {
		return &exitError{Code: 1, Msg: "decode prune plan", Err: err}
	}
	if err := printPrunePlan(cmd.OutOrStdout(), plan); err != nil {
		return err
	}
	if detailed && len(plan.WouldDelete) > 0 {
		return &exitError{Code: 2, Msg: fmt.Sprintf("%d team(s) would be deleted", len(plan.WouldDelete))}
	}
	return nil
}

// printPrunePlan renders a prune plan in the groups of
// docs/design/work-prune.md section 4. A group with nothing in it is left out.
func printPrunePlan(w io.Writer, plan api.PrunePlan) error {
	var b strings.Builder
	fmt.Fprintf(&b, "prune plan for workspace/%s from %s\n", plan.Workspace, plan.Source)
	fmt.Fprintf(&b, "digest %s\n", plan.Digest)
	fmt.Fprintln(&b, "pre-flight checks not evaluated (aae-orc-qawkf)")
	if len(plan.WouldDelete) == 0 {
		fmt.Fprintln(&b, "\nwould delete: nothing")
	} else {
		fmt.Fprintln(&b, "\nwould delete (claimed only by this file, which no longer names them):")
		for _, d := range plan.WouldDelete {
			line := "  " + d.Team + "   " + seatSummary(d.Seats)
			for _, note := range []string{d.Handoff, d.Headless} {
				if note != "" {
					line += "   " + note
				}
			}
			fmt.Fprintln(&b, line)
		}
	}
	if len(plan.KeptShared) > 0 {
		fmt.Fprintln(&b, "\nkept, claimed by another file too:")
		for _, k := range plan.KeptShared {
			sources := make([]string, 0, len(k.Others))
			for s := range k.Others {
				sources = append(sources, s)
			}
			sort.Strings(sources)
			also := make([]string, 0, len(sources))
			for _, s := range sources {
				also = append(also, fmt.Sprintf("%s (applied %s)", s, k.Others[s].UTC().Format("2006-01-02T15:04Z")))
			}
			fmt.Fprintf(&b, "  %s   also %s\n", k.Team, strings.Join(also, "; "))
		}
	}
	if len(plan.KeptUnclaimed) > 0 {
		fmt.Fprintln(&b, "\nkept, unclaimed (applied before claims existed, or by an older client):")
		for _, k := range plan.KeptUnclaimed {
			fmt.Fprintf(&b, "  %s   delete by hand: marvel delete team %s\n", k, k)
		}
	}
	if len(plan.KeptRun) > 0 {
		fmt.Fprintln(&b, "\nkept, holds marvel run sessions:")
		for _, k := range plan.KeptRun {
			fmt.Fprintf(&b, "  %s   %s (not the manifest's to remove)\n", k.Team, plural(k.Runs, "run session"))
		}
	}
	if len(plan.Deferred) > 0 {
		fmt.Fprintln(&b, "\ndeferred:")
		for _, k := range plan.Deferred {
			fmt.Fprintf(&b, "  %s   %s\n", k.Team, k.Reason)
		}
	}
	if len(plan.Held) > 0 {
		fmt.Fprintln(&b, "\nnamed by this file and held (re-apply converges them):")
		for _, k := range plan.Held {
			fmt.Fprintf(&b, "  %s   will spawn %d\n", k.Team, k.WillSpawn)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// seatSummary is "N seats" and, when there are any, how many are in each state.
func seatSummary(seats []api.PruneSeat) string {
	out := plural(len(seats), "seat")
	if len(seats) == 0 {
		return out
	}
	counts := map[api.SessionState]int{}
	for _, s := range seats {
		counts[s.State]++
	}
	states := make([]string, 0, len(counts))
	for st := range counts {
		states = append(states, string(st))
	}
	sort.Strings(states)
	parts := make([]string, 0, len(states))
	for _, st := range states {
		parts = append(parts, fmt.Sprintf("%d %s", counts[api.SessionState(st)], st))
	}
	return out + "  " + strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
