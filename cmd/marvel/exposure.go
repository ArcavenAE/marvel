package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// exposureCmd records which function's source a slot has read, so a role that
// writes it is not started there (marvel#945). The slot is workspace/team and
// a role; the function name is a closed class, refused here before it is sent.
func exposureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exposure",
		Short: "Record, end or void the functions a slot has read (the clean-room exposure gate)",
		Long: `A role that declares writes: [function] is not started while its slot
(workspace, team, role) has an exposure record for that function. The record
is kept outside the session rows, so a restart, a crash respawn, a shift and an
ad-hoc run all see it.

  marvel exposure add  <workspace/team> <role> <function> [--path P]... [--caster WHO]
  marvel exposure end  <workspace/team> <role> <function>
  marvel exposure void <workspace/team> <role> <function> --reason WHY

end marks a reading as ended; the refusal still counts it. void is the way out
of a wrong record: operator-run on the local socket only, it appends a reason
and the gate stops counting the record. Nothing is ever deleted.`,
	}

	var paths []string
	var caster string
	add := &cobra.Command{
		Use:   "add <workspace/team> <role> <function>",
		Short: "Record that the slot read a function",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := exposureSlotParams(args)
			if err != nil {
				return err
			}
			p["paths"] = paths
			if caster != "" {
				p["caster"] = caster
			}
			return exposureSend(cmd, "exposure.add", p, "recorded")
		},
	}
	add.Flags().StringArrayVar(&paths, "path", nil, "a path that was read (repeatable)")
	add.Flags().StringVar(&caster, "caster", "", "who records it (default: the caller)")
	cmd.AddCommand(add)

	end := &cobra.Command{
		Use:   "end <workspace/team> <role> <function>",
		Short: "Mark the slot's readings of a function as ended (they still count)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := exposureSlotParams(args)
			if err != nil {
				return err
			}
			return exposureSend(cmd, "exposure.end", p, "ended")
		},
	}
	cmd.AddCommand(end)

	var reason string
	void := &cobra.Command{
		Use:   "void <workspace/team> <role> <function> --reason WHY",
		Short: "Void a wrong record: the gate stops counting it; nothing is deleted (local socket only)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := exposureSlotParams(args)
			if err != nil {
				return err
			}
			if strings.TrimSpace(reason) == "" {
				return fmt.Errorf("--reason is required: say why the record was wrong")
			}
			p["reason"] = reason
			return exposureSend(cmd, "exposure.void", p, "voided")
		},
	}
	void.Flags().StringVar(&reason, "reason", "", "why the record was wrong (required)")
	cmd.AddCommand(void)
	return cmd
}

// exposureSlotParams reads <workspace/team> <role> <function> and refuses a
// slot or a name the daemon would refuse, before anything is sent.
func exposureSlotParams(args []string) (map[string]any, error) {
	ws, team, ok := strings.Cut(args[0], "/")
	if !ok || ws == "" || team == "" || strings.Contains(team, "/") {
		return nil, fmt.Errorf("name the team as workspace/team, got %q", args[0])
	}
	if args[1] == "" {
		return nil, fmt.Errorf("name the role")
	}
	if _, err := api.CheckFunction(args[2]); err != nil {
		return nil, err
	}
	return map[string]any{"workspace": ws, "team": team, "role": args[1], "function": args[2]}, nil
}

func exposureSend(cmd *cobra.Command, method string, p map[string]any, verb string) error {
	params, _ := json.Marshal(p)
	resp, err := send(daemonrpc.Request{Method: method, Params: params})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "exposure %s: %s/%s %s %s\n", verb, p["workspace"], p["team"], p["role"], p["function"])
	return nil
}
