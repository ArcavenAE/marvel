package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/spf13/cobra"
)

func backendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backend",
		Short: "Inspect the model backend sessions are running on",
	}
	cmd.AddCommand(backendVerifyCmd())
	return cmd
}

func backendVerifyCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "verify [workspace/session]",
		Short: "Name each session's backend and report any disagreement",
		Long: `Name the backend each session is actually on, and report every layer that
disagrees.

macOS does not let one process read another's environment, so this does not ask
a live pane what backend it ended up on. It verifies against the artifacts
marvel controls: the settings overlay marvel wrote and passed to the harness,
and the intended/resolved pair marvel recorded when the session spawned.

Four things can disagree, and each is reported by name:

  - marvel never classified the session (an adopted pane, or a record written
    before the field existed), so the answer is "cannot tell" rather than a pass
  - the spawn environment resolved to a backend the role did not declare
  - the overlay on disk selects something other than what was declared, or
    something other than what marvel recorded at spawn
  - an IAM session has no refresh marvel can vouch for, so a credential expiry
    mid-shift will need a human

The RESOLVED column names the IAM or static variant for the AWS backends. The
selector cannot distinguish them, so the variant is named from the credential
source the role declared: a helper-script path or a profile name, never a
credential.

Exits non-zero when any session reports a problem, so a shakedown can gate on
it.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			session := ""
			if len(args) == 1 {
				session = args[0]
			}
			params, err := json.Marshal(map[string]string{"session": session})
			if err != nil {
				return fmt.Errorf("encode request: %w", err)
			}
			resp, err := send(daemon.Request{Method: "backend.verify", Params: params})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			var report []api.BackendVerification
			if err := json.Unmarshal(resp.Result, &report); err != nil {
				return fmt.Errorf("decode backend verification: %w", err)
			}
			if jsonOut {
				out, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return fmt.Errorf("encode backend verification: %w", err)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(out))
			} else {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), renderBackendVerifications(report))
			}
			failed := 0
			for _, v := range report {
				if !v.OK {
					failed++
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d sessions failed backend verification", failed, len(report))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the full verification records as JSON")
	return cmd
}

// renderBackendVerifications draws the report. Split out from the command so
// the rendering is testable without a daemon.
func renderBackendVerifications(report []api.BackendVerification) string {
	if len(report) == 0 {
		return "No sessions to verify.\n"
	}
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "SESSION\tINTENDED\tRESOLVED\tOVERLAY\tCREDENTIALS\tVERDICT\n")
	for _, v := range report {
		verdict := "ok"
		if !v.OK {
			verdict = "PROBLEM"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			v.Session,
			backendCell(string(v.Intended)),
			backendCell(v.Label),
			backendOverlayCell(v),
			backendCell(string(v.CredentialSource)),
			verdict)
	}
	_ = w.Flush()

	// Problems go under the table rather than in a cell: they are sentences,
	// and the operator needs the whole sentence to act on one.
	for _, v := range report {
		for _, p := range v.Problems {
			fmt.Fprintf(&buf, "\n%s: %s", v.Session, p)
		}
	}
	if buf.Len() > 0 && buf.Bytes()[buf.Len()-1] != '\n' {
		buf.WriteByte('\n')
	}
	return buf.String()
}

// backendCell renders an unset field as a dash rather than an empty cell, so a
// missing value reads as absent instead of as a rendering slip.
func backendCell(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// backendOverlayCell distinguishes an overlay that was never warranted from one
// that should be there and is not - the same distinction VerifyBackend draws.
func backendOverlayCell(v api.BackendVerification) string {
	switch {
	case v.OverlayPresent:
		return string(v.Overlay)
	case api.BackendOverlayWarranted(v.Intended):
		return "MISSING"
	default:
		return "none"
	}
}
