package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/hostvitals"
)

// `get vitals` across a cluster set follows the get sessions fan-out: every
// cluster is read in parallel under its own timer, and one that cannot be read
// is reported with its outcome, never as a reading of zeros.

// clusterVitals is one cluster's snapshot in the -o json document.
type clusterVitals struct {
	Cluster  string              `json:"cluster"`
	Snapshot hostvitals.Snapshot `json:"snapshot"`
}

// vitalsFanOutDocument is what `get vitals --all-clusters -o json` prints: one
// outcome record per cluster in the order named, and a snapshot for each
// cluster that answered.
type vitalsFanOutDocument struct {
	Clusters []clusterRecord `json:"clusters"`
	Vitals   []clusterVitals `json:"vitals"`
}

// readClusterVitals asks one cluster for its host vitals under the timer. The
// snapshot is nil unless the cluster answered.
func readClusterVitals(t clusterTarget, timer time.Duration) (clusterRecord, *hostvitals.Snapshot) {
	rec := clusterRecord{Cluster: t.Name}
	finish := func() (clusterRecord, *hostvitals.Snapshot) {
		rec.AsOf = time.Now().UTC().Format(time.RFC3339)
		if !rec.answered() && rec.Next == "" {
			rec.Next = nextStep(rec.Outcome)
		}
		return rec, nil
	}
	opts := t.Opts
	opts.StrictHostKey = true
	opts.OfferOneKey = true
	opts.Timeout = timer
	resp, err := daemonrpc.SendRequestWith(t.Addr, daemonrpc.Request{Method: daemonrpc.MethodHostVitals}, opts)
	if err != nil {
		rec.Outcome, rec.Phase, rec.Fingerprint, rec.Cause = classifyDialError(err, timer)
		var noID *daemonrpc.NoIdentityError
		if errors.As(err, &noID) {
			rec.Next = "no key could be loaded, so no dial was made; give the entry an identity or run 'marvel keys generate'"
		}
		return finish()
	}
	if resp.Error != "" {
		rec.Cause = resp.Error
		rec.Outcome = outcomeDaemonError
		if resp.Starting {
			rec.Outcome = outcomeStarting
		}
		return finish()
	}
	var snap hostvitals.Snapshot
	if err := json.Unmarshal(resp.Result, &snap); err != nil {
		rec.Outcome, rec.Cause = outcomeBadReply, "the reply did not parse as host vitals"
		return finish()
	}
	rec.Outcome = outcomeAnswered
	if resp.Starting {
		rec.Outcome = outcomeStarting
	}
	rec.AsOf = time.Now().UTC().Format(time.RFC3339)
	rec.Next = nextStep(rec.Outcome)
	return rec, &snap
}

// collectFanOutVitals resolves every named cluster from its own config entry,
// then reads them all in parallel. Results come back in the order of names.
func collectFanOutVitals(names []string) ([]clusterRecord, []*hostvitals.Snapshot, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("the client config is unreadable: %w", err)
	}
	targets := make([]clusterTarget, 0, len(names))
	for _, n := range names {
		t, err := resolveCluster(cfg, n)
		if err != nil {
			return nil, nil, err
		}
		targets = append(targets, t)
	}
	records := make([]clusterRecord, len(targets))
	snaps := make([]*hostvitals.Snapshot, len(targets))
	timer := fanOutTimeout
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			records[i], snaps[i] = readClusterVitals(t, timer)
		}()
	}
	wg.Wait()
	return records, snaps, nil
}

// runFanOutVitals is `get vitals` across a cluster set.
func runFanOutVitals(cmd *cobra.Command, names []string, format string, hasOutput, watching bool, columns string, load bool) error {
	if err := fanOutRefusals(); err != nil {
		return err
	}
	switch {
	case load:
		return errors.New("--load applies to get teams only")
	case watching:
		return errors.New("get vitals does not support --watch")
	case columns != "":
		return errors.New("--columns applies to get sessions only")
	case hasOutput && format != "json":
		return fmt.Errorf("get vitals prints json with -o json; -o %s is not supported", format)
	}
	records, snaps, err := collectFanOutVitals(names)
	if err != nil {
		return err
	}
	if hasOutput {
		doc := vitalsFanOutDocument{Clusters: records, Vitals: []clusterVitals{}}
		for i, s := range snaps {
			if s != nil {
				doc.Vitals = append(doc.Vitals, clusterVitals{Cluster: records[i].Cluster, Snapshot: *s})
			}
		}
		out, err := renderGetOutput(doc, "json")
		if err != nil {
			return err
		}
		fmt.Print(out)
		return fanOutExitError(cmd, records)
	}
	if err := printFanOutVitals(cmd.OutOrStdout(), records, snaps, time.Now()); err != nil {
		return err
	}
	return fanOutExitError(cmd, records)
}

// printFanOutVitals writes one block per cluster. A cluster that gave no
// reading gets a gap line with its outcome and the next step.
func printFanOutVitals(w io.Writer, records []clusterRecord, snaps []*hostvitals.Snapshot, now time.Time) error {
	for i, r := range records {
		if snaps[i] == nil {
			line := fmt.Sprintf("cluster %s: GAP %s: %s", r.Cluster, r.Outcome, drawn(r.Cause))
			if r.Next != "" {
				line += "; " + r.Next
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "cluster %s:\n", r.Cluster); err != nil {
			return err
		}
		if err := printVitals(w, *snaps[i], now); err != nil {
			return err
		}
	}
	return nil
}
