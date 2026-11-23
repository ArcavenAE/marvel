package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// getAccountReadings is `marvel get account-readings [--account] [--since]`: the
// stamped readings of every account window the daemon holds, which come back
// from disk after a restart. It reads the history only; nothing asks a
// harness and nothing gates on it.
func getAccountReadings(w io.Writer, account string, since time.Duration, output string) error {
	if output != "" && output != "json" {
		return fmt.Errorf("get account-readings prints json with -o json; -o %s is not supported", output)
	}
	if since < 0 {
		return fmt.Errorf("--since must not be negative")
	}
	// The daemon takes whole seconds and reads zero as "every reading", so a
	// positive request under a second would be sent as zero and list everything.
	// It is refused here, where the operator can see why, and not rounded up.
	if since > 0 && since < time.Second {
		return fmt.Errorf("--since must be at least 1s, or leave it out to list every reading")
	}
	params, err := json.Marshal(daemonrpc.GetParams{
		ResourceType: "account-readings",
		Account:      account,
		SinceSeconds: int64(since / time.Second),
	})
	if err != nil {
		return err
	}
	resp, err := sendTimed(daemonrpc.Request{Method: "get", Params: params})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	var samples []api.AccountSample
	if err := json.Unmarshal(resp.Result, &samples); err != nil {
		return fmt.Errorf("decode account readings: %w", err)
	}
	if output == "json" {
		out, err := json.MarshalIndent(samples, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(out))
		return err
	}
	return printAccountReadings(w, samples)
}

// printAccountReadings writes one row per reading: the account, the window, the
// percent used, the reset time in UTC, when it was observed, and the session
// that reported it. A reading with no reset time shows a dash.
func printAccountReadings(w io.Writer, samples []api.AccountSample) error {
	if len(samples) == 0 {
		_, err := fmt.Fprintln(w, "no account readings")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ACCOUNT\tWINDOW\tUSED%\tRESETS\tOBSERVED\tSESSION")
	for _, s := range samples {
		resets := "-"
		if !s.ResetsAt.IsZero() {
			resets = s.ResetsAt.UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%.1f\t%s\t%s\t%s\n",
			s.Account, s.Window, s.UsedPercent, resets, s.At.UTC().Format(time.RFC3339), dashIfEmpty(s.Session))
	}
	return tw.Flush()
}
