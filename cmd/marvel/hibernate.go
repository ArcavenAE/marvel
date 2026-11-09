package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// hibernatePoll and hibernateWait are how often a verb reads status and how
// long it waits for the teams to settle; tests replace them.
var (
	hibernatePoll = 2 * time.Second
	hibernateWait = 10 * time.Minute
)

// The reply shapes, as the daemon sends them (internal/daemon/hibernate.go).
type hibernateTeamReply struct {
	Team     string   `json:"team"`
	State    string   `json:"state"`
	Running  int      `json:"running"`
	Pending  []string `json:"pending"`
	Reported []string `json:"reported"`
	Done     bool     `json:"done"`
}

type hibernateReply struct {
	Teams  []hibernateTeamReply `json:"teams"`
	Labels map[string]int       `json:"labels"`
	Seats  int                  `json:"seats"`
}

type wakeReply struct {
	Checks []struct {
		Name    string `json:"name"`
		Result  string `json:"result"`
		Detail  string `json:"detail"`
		Skipped bool   `json:"skipped"`
	} `json:"checks"`
	Teams []hibernateTeamReply `json:"teams"`
}

// hibernateCmd is marvel hibernate: sign off the named teams in order and mark
// them hibernated (docs/design/fleet-hibernate.md sections 3, 4 and 7).
func hibernateCmd() *cobra.Command {
	var (
		all, yes, noWait bool
		end              string
		backend, except  []string
	)
	cmd := &cobra.Command{
		Use:   "hibernate <workspace/team>... | --all | --end <session>",
		Short: "Sign off the named teams, end every seat, and hold the teams until a wake",
		Long: `Ask every seat of the named teams for its handoff, end each seat once its
handoff ends in its marker, and mark the team hibernated so nothing respawns it.
Workers are asked together, then the supervisors, once every worker has ended or
been reported. A seat that writes no handoff is never ended by this command: it
is reported by name and keeps running. marvel hibernate --end <session> ends that
one seat without a handoff, which loses its context.

Seats return as successors that read their handoff: marvel wake starts each one
fresh, and nothing carries over except that handoff.

--backend and --except-backend select roles by their declared backend label;
the word "none" names a role with no label. A selected subset is marked per role,
and the rest of the team keeps running.

On a terminal it states how many seats it signs off, per label, and asks [y/N].
With no terminal it refuses unless --yes is given. It waits until every team has
settled or every remaining seat is reported; --no-wait returns at once.

Exit codes: 0 hibernated, 3 a seat was reported with no handoff, 1 error.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in := cmd.InOrStdin()
			f, ok := in.(*os.File)
			interactive := ok && term.IsTerminal(int(f.Fd()))
			p := map[string]any{"teams": args, "all": all, "backend": backend, "except_backend": except}
			code, err := runHibernate(cmd.OutOrStdout(), in, interactive, yes, noWait, end, p, send)
			return verbExit(code, err)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "hibernate every team")
	cmd.Flags().BoolVar(&yes, "yes", false, "hibernate without asking")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return once the sign-off has started")
	cmd.Flags().StringVar(&end, "end", "", "end this seat without a handoff (a seat of a hibernating team)")
	cmd.Flags().StringSliceVar(&backend, "backend", nil, "hibernate only roles on these backend labels (none names an unlabeled role)")
	cmd.Flags().StringSliceVar(&except, "except-backend", nil, "hibernate every role except those on these backend labels")
	return cmd
}

// wakeCmd is marvel wake: preflight, then bring hibernated teams back in order.
func wakeCmd() *cobra.Command {
	var (
		all, yes bool
		skip     []string
		order    []string
	)
	cmd := &cobra.Command{
		Use:   "wake <workspace/team>... | --all",
		Short: "Bring hibernated teams back, supervisors first",
		Long: `Run the preflight, then wake each hibernated team: the supervisor roles start
first, and the other roles once a supervisor has heartbeated, or after its health
timeout. Every seat starts fresh as a successor of its hibernated session and
reads its handoff. Running wake again on a waking team is safe: it starts no seat
twice.

The preflight checks that the daemon answers, that the bus links the cluster
config declares are up, and that each harness binary is present. A check is
skipped by name with --skip-check (daemon, bus, harness-binary), and the skip is
recorded on the wake event. Credentials are not read.

On a terminal it asks [y/N]; with no terminal it refuses unless --yes is given.
--order wakes the named teams one after another.

Exit codes: 0 woken, 3 a team was still waking when the wait ended, 1 error.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in := cmd.InOrStdin()
			f, ok := in.(*os.File)
			interactive := ok && term.IsTerminal(int(f.Fd()))
			p := map[string]any{"teams": args, "all": all, "skip_checks": skip, "order": order}
			code, err := runWake(cmd.OutOrStdout(), in, interactive, yes, p, send)
			return verbExit(code, err)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "wake every hibernated team")
	cmd.Flags().BoolVar(&yes, "yes", false, "wake without asking")
	cmd.Flags().StringSliceVar(&skip, "skip-check", nil, "skip a preflight check by name")
	cmd.Flags().StringSliceVar(&order, "order", nil, "wake these teams one after another, in this order")
	return cmd
}

// confirm asks [y/N] on a terminal and refuses without one unless yes is set.
func confirm(out io.Writer, in io.Reader, interactive, yes bool, verb, prompt string) error {
	if yes {
		return nil
	}
	if !interactive {
		return fmt.Errorf("%s needs a terminal to confirm; run it in one, or pass --yes. Nothing was changed", verb)
	}
	_, _ = fmt.Fprint(out, prompt+" [y/N] ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read answer: %w", err)
	}
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return errors.New("not confirmed; nothing was changed")
	}
	return nil
}

func callHibernate(call rpcCall, method string, p map[string]any) (json.RawMessage, error) {
	params, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	resp, err := call(daemonrpc.Request{Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	if err := verbRefusal(resp, strings.TrimPrefix(method, "team.")); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func labelLine(labels map[string]int) string {
	names := make([]string, 0, len(labels))
	for l := range labels {
		names = append(names, l)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, l := range names {
		parts[i] = fmt.Sprintf("%s %d", l, labels[l])
	}
	return strings.Join(parts, ", ")
}

// runHibernate confirms, starts the hibernate, and waits for it to settle.
func runHibernate(out io.Writer, in io.Reader, interactive, yes, noWait bool, end string, p map[string]any, call rpcCall) (int, error) {
	if end != "" {
		if _, err := callHibernate(call, "team.hibernate", map[string]any{"end": end}); err != nil {
			return 1, err
		}
		_, _ = fmt.Fprintf(out, "ended %s without a handoff\n", end)
		return 0, nil
	}
	dry := map[string]any{"dry_run": true}
	for k, v := range p {
		dry[k] = v
	}
	raw, err := callHibernate(call, "team.hibernate", dry)
	if err != nil {
		return 1, err
	}
	var count hibernateReply
	if err := json.Unmarshal(raw, &count); err != nil {
		return 1, fmt.Errorf("decode hibernate count: %w", err)
	}
	prompt := fmt.Sprintf("This asks %d seats (%s) for a handoff and ends them; the teams stay down until marvel wake.\nHibernate?", count.Seats, labelLine(count.Labels))
	if err := confirm(out, in, interactive, yes, "hibernate", prompt); err != nil {
		return 1, err
	}
	raw, err = callHibernate(call, "team.hibernate", p)
	if err != nil {
		return 1, err
	}
	var start hibernateReply
	if err := json.Unmarshal(raw, &start); err != nil {
		return 1, fmt.Errorf("decode hibernate result: %w", err)
	}
	keys := make([]string, 0, len(start.Teams))
	for _, t := range start.Teams {
		keys = append(keys, t.Team)
	}
	_, _ = fmt.Fprintf(out, "hibernate started for %s (%d seats asked)\n", strings.Join(keys, ", "), count.Seats)
	if noWait {
		return 0, nil
	}
	deadline := time.Now().Add(hibernateWait)
	for {
		raw, err := callHibernate(call, "team.hibernate", map[string]any{"status": true, "teams": keys})
		if err != nil {
			return 1, err
		}
		var st hibernateReply
		if err := json.Unmarshal(raw, &st); err != nil {
			return 1, fmt.Errorf("decode hibernate status: %w", err)
		}
		settled, reported := true, []string{}
		for _, t := range st.Teams {
			reported = append(reported, t.Reported...)
			if !t.Done && (len(t.Pending) != 0 || t.Running > len(t.Reported)) {
				settled = false
			}
		}
		if settled || time.Now().After(deadline) {
			if len(reported) > 0 {
				_, _ = fmt.Fprintf(out, "reported with no handoff, still running: %s\n", strings.Join(reported, ", "))
				_, _ = fmt.Fprintln(out, "wait for them, or end one with: marvel hibernate --end <session>")
				return 3, nil
			}
			if !settled {
				return 3, fmt.Errorf("still signing off after %s; run again to keep waiting", hibernateWait)
			}
			_, _ = fmt.Fprintln(out, "hibernated")
			return 0, nil
		}
		time.Sleep(hibernatePoll)
	}
}

// runWake confirms, runs the wake, and waits for the marks to clear.
func runWake(out io.Writer, in io.Reader, interactive, yes bool, p map[string]any, call rpcCall) (int, error) {
	if err := confirm(out, in, interactive, yes, "wake", "This starts every seat of the named hibernated teams as a fresh successor.\nWake?"); err != nil {
		return 1, err
	}
	raw, err := callHibernate(call, "team.wake", p)
	if err != nil {
		return 1, err
	}
	var res wakeReply
	if err := json.Unmarshal(raw, &res); err != nil {
		return 1, fmt.Errorf("decode wake result: %w", err)
	}
	for _, c := range res.Checks {
		note := c.Result
		if c.Skipped {
			note = "skipped"
		}
		_, _ = fmt.Fprintf(out, "check %s: %s (%s)\n", c.Name, note, c.Detail)
	}
	keys := make([]string, 0, len(res.Teams))
	for _, t := range res.Teams {
		keys = append(keys, t.Team)
	}
	deadline := time.Now().Add(hibernateWait)
	for {
		raw, err := callHibernate(call, "team.hibernate", map[string]any{"status": true, "teams": keys})
		if err != nil {
			return 1, err
		}
		var st hibernateReply
		if err := json.Unmarshal(raw, &st); err != nil {
			return 1, fmt.Errorf("decode wake status: %w", err)
		}
		var waking []string
		for _, t := range st.Teams {
			waking = append(waking, t.Team)
		}
		if len(waking) == 0 {
			_, _ = fmt.Fprintf(out, "woken: %s\n", strings.Join(keys, ", "))
			return 0, nil
		}
		if time.Now().After(deadline) {
			_, _ = fmt.Fprintf(out, "still waking after %s: %s; run wake again to carry on\n", hibernateWait, strings.Join(waking, ", "))
			return 3, nil
		}
		time.Sleep(hibernatePoll)
	}
}

// reportHibernated lists the hibernated and waking teams beside the suspend
// state in marvel status. It is best effort: a daemon that predates the verbs,
// or does not answer, adds no line and changes no exit code.
func reportHibernated(w io.Writer, call rpcCall) {
	raw, err := callHibernate(call, "team.hibernate", map[string]any{"status": true})
	if err != nil {
		return
	}
	var st hibernateReply
	if json.Unmarshal(raw, &st) != nil {
		return
	}
	for _, t := range st.Teams {
		line := fmt.Sprintf("team %s: %s", t.Team, t.State)
		if t.State == "hibernated" && !t.Done {
			line += fmt.Sprintf(" (signing off: %d running, %d reported)", t.Running, len(t.Reported))
		}
		_, _ = fmt.Fprintln(w, line)
	}
}
