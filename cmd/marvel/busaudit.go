package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

const auditSeqText = "a sequence number, a whole number of 1 or more"

// parseAuditSeq reads the one argument of `bus audit`.
func parseAuditSeq(args []string) (uint64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("marvel bus audit needs one argument: %s", auditSeqText)
	}
	n, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%q is not %s", escapeAll(args[0]), auditSeqText)
	}
	return n, nil
}

func busAuditCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "audit <seq>",
		Short: "Read one record from the broker's audit log by number",
		Long: `Read one record from the broker's audit log by number.

A send that is refused or fails on a cluster's bus leaves a numbered record in
that broker's AGENT_AUDIT stream, and the sender is told the number. This reads
it through marvel's own admin login to the broker, so it works only where marvel
runs the broker. With --cluster it reads another cluster's record, because it
runs on that cluster's daemon.

A record holds the full envelope of a send, text included, and any admin caller
on that daemon can read any record by number.

Text from the record is escaped, never printed raw: a character a terminal
could act on prints as \uXXXX. Exit 0 on a record, 1 on a refusal or a number
with no record.`,
		Args: func(cmd *cobra.Command, args []string) error {
			_, err := parseAuditSeq(args)
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			seq, err := parseAuditSeq(args)
			if err != nil {
				return err
			}
			if output != "" && output != "json" {
				return fmt.Errorf("unsupported output %q; use json", escapeAll(output))
			}
			cmd.SilenceUsage = true
			params, err := json.Marshal(map[string]uint64{"seq": seq})
			if err != nil {
				return err
			}
			resp, err := send(daemonrpc.Request{Method: "bus.audit", Params: params})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return errors.New(escapeAll(resp.Error))
			}
			var rec bus.AuditRecord
			if err := json.Unmarshal(resp.Result, &rec); err != nil {
				return fmt.Errorf("decode audit record: %w", err)
			}
			return printBusAudit(cmd.OutOrStdout(), rec, output == "json")
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "print the record as json")
	return cmd
}

// auditView is the record as printed under -o json.
type auditView struct {
	Seq      uint64 `json:"seq"`
	Time     string `json:"time"`
	Subject  string `json:"subject"`
	Outcome  string `json:"outcome"`
	Stage    string `json:"stage,omitempty"`
	Refusal  string `json:"refusal,omitempty"`
	MsgID    string `json:"msg_id"`
	Envelope any    `json:"envelope"`
}

// auditEnvelope decodes the body when it is JSON, so it prints as a value, and
// otherwise keeps it as text.
func auditEnvelope(body []byte) (value any, isJSON bool) {
	if !json.Valid(body) {
		return string(body), false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return string(body), false
	}
	return value, true
}

// printBusAudit writes one record as key and value lines, or as json. Every
// string from the record leaves through an escaping pass.
func printBusAudit(w io.Writer, rec bus.AuditRecord, asJSON bool) error {
	refused := rec.Outcome == "refused"
	outcome := "sent"
	if refused {
		outcome = "refused"
	}
	env, isJSON := auditEnvelope(rec.Body)
	stamp := rec.Time.UTC().Format(time.RFC3339)

	if asJSON {
		view := auditView{
			Seq: rec.Seq, Time: stamp, Subject: rec.Subject, Outcome: outcome,
			MsgID: rec.MsgID, Envelope: env,
		}
		if refused {
			view.Stage, view.Refusal = rec.Stage, rec.Refusal
		}
		out, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return fmt.Errorf("encode audit record: %w", err)
		}
		_, err = fmt.Fprintln(w, escapeInStrings(string(out)))
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "seq:      %d\n", rec.Seq)
	fmt.Fprintf(&b, "time:     %s\n", stamp)
	fmt.Fprintf(&b, "subject:  %s\n", escapeAll(rec.Subject))
	fmt.Fprintf(&b, "outcome:  %s\n", outcome)
	if refused {
		fmt.Fprintf(&b, "stage:    %s\n", escapeAll(rec.Stage))
		fmt.Fprintf(&b, "refusal:  %s\n", strconv.Quote(rec.Refusal))
	}
	fmt.Fprintf(&b, "msg_id:   %s\n", strconv.Quote(rec.MsgID))
	b.WriteString("envelope:\n")
	if isJSON {
		out, err := json.MarshalIndent(env, "  ", "  ")
		if err != nil {
			return fmt.Errorf("encode audit envelope: %w", err)
		}
		b.WriteString("  " + escapeInStrings(string(out)) + "\n")
	} else {
		b.WriteString("  " + strconv.Quote(string(rec.Body)) + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
