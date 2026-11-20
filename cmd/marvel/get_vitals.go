package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/hostvitals"
)

// getVitals prints what `marvel get vitals` shows: the daemon's latest host
// vitals sample, its time, each threshold with its state and each gap line.
// It makes one host.vitals call, which the daemon answers from memory. Every
// figure is diagnostic: nothing gates or acts on it. output is "" for the
// table or "json" for the daemon's snapshot as a document.
func getVitals(w io.Writer, output string) error {
	if output != "" && output != "json" {
		return fmt.Errorf("get vitals prints json with -o json; -o %s is not supported", output)
	}
	resp, err := send(daemonrpc.Request{Method: daemonrpc.MethodHostVitals})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	var snap hostvitals.Snapshot
	if err := json.Unmarshal(resp.Result, &snap); err != nil {
		return fmt.Errorf("decode host vitals: %w", err)
	}
	if output == "json" {
		out, err := json.MarshalIndent(snap, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(out))
		return err
	}
	return printVitals(w, snap, time.Now())
}

// printVitals writes the table form of a snapshot. now is the reader's clock,
// used only for the age of the sample.
func printVitals(w io.Writer, snap hostvitals.Snapshot, now time.Time) error {
	switch {
	case !snap.Enabled:
		_, err := fmt.Fprintln(w, "host vitals is off on this daemon: set host_vitals: true on its cluster in the marvel config to turn it on (docs/design/host-vitals.md)")
		return err
	case !snap.Sampled:
		if _, err := fmt.Fprintln(w, "host vitals is on and has taken no sample yet; the first reading comes with the daemon's first metrics pass"); err != nil {
			return err
		}
	default:
		age := ""
		if snap.At != nil {
			age = fmt.Sprintf(", sampled %s (%s ago)", snap.At.UTC().Format(time.RFC3339), now.Sub(*snap.At).Round(time.Second))
		}
		if _, err := fmt.Fprintf(w, "host vitals%s\n", age); err != nil {
			return err
		}
		for _, t := range snap.Thresholds {
			if _, err := fmt.Fprintf(w, "  %-16s line: %-20s %-6s %s\n", t.Name, t.Line, t.State, t.Detail); err != nil {
				return err
			}
		}
	}
	for _, g := range snap.Gaps {
		if _, err := fmt.Fprintf(w, "GAP %s: %s (docs/design/host-vitals.md section %s)\n", g.Vital, g.Reason, g.Section); err != nil {
			return err
		}
	}
	return nil
}
