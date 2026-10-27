package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// The same-daemon merge (docs/design/multicluster-read-view-plan.md, section
// 3.3). Two config entries can reach one daemon, and a view that read both
// would list its sessions twice. A miss costs duplicate rows; a false merge
// would label one cluster's rows with another's name, so a merge needs
// evidence the daemon cannot forge: the same address, the same socket inode,
// or the same host key the operator verified, and only then does the pid and
// start time the daemon reports have to agree.

// addressKey is what makes a second dial redundant: the same address read
// with the same identity. Two spellings of one unix path compare equal; other
// schemes compare as written. The identity is part of the key because the
// same address read with another key can answer differently, and a refusal is
// news the second entry must still report.
func addressKey(t clusterTarget) string {
	if daemonrpc.IsSSH(t.Addr) || daemonrpc.IsMRVL(t.Addr) || hasScheme(t.Addr) {
		return t.Addr + "\x00" + t.Opts.Identity
	}
	return filepath.Clean(t.Addr)
}

func hasScheme(addr string) bool {
	u, err := url.Parse(addr)
	return err == nil && u.Scheme != ""
}

// firstAddress maps each target to the index of the first target with the same
// address, which is itself when no earlier one has it.
func firstAddress(targets []clusterTarget) []int {
	first := make([]int, len(targets))
	seen := map[string]int{}
	for i, t := range targets {
		k := addressKey(t)
		if j, ok := seen[k]; ok {
			first[i] = j
			continue
		}
		seen[k] = i
		first[i] = i
	}
	return first
}

// recordForRepeatedAddress is the record of a target whose address was
// already dialled: it shares the first dial's fate. When that dial gave rows
// the entry reads same_daemon and names the first.
func recordForRepeatedAddress(name string, first clusterRecord) clusterRecord {
	rec := first
	rec.Cluster = name
	rec.Sessions = nil
	if first.answered() {
		rec.Outcome = outcomeSameDaemon
		rec.SameDaemonAs = first.Cluster
		rec.Cause = "same address as " + first.Cluster + ", not dialled again"
		rec.Next = ""
	}
	return rec
}

// daemonKey is what two entries must share before their daemons are
// compared: the socket's device and inode for a unix entry, and the verified
// host key (plus the remote socket path for ssh://) for an entry that dialled
// over ssh. Empty means the entry offers nothing to merge on.
func daemonKey(addr, fingerprint string) string {
	switch {
	case daemonrpc.IsSSH(addr) || daemonrpc.IsMRVL(addr):
		if fingerprint == "" {
			return ""
		}
		u, err := url.Parse(addr)
		if err != nil {
			return ""
		}
		return "ssh " + fingerprint + " " + u.Path
	case hasScheme(addr):
		return ""
	}
	fi, err := os.Stat(addr)
	if err != nil {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("unix %d %d", st.Dev, st.Ino)
}

// daemonStamp is the pid and start time a daemon reports to version.
type daemonStamp struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

func (s daemonStamp) usable() bool { return s.PID != 0 && !s.StartedAt.IsZero() }

// readStamp asks a daemon for its stamp under the fan-out timer.
func readStamp(t clusterTarget) (daemonStamp, bool) { return readStampWithin(t, fanOutTimeout) }

// readStampWithin asks a daemon for its stamp under the given timer.
func readStampWithin(t clusterTarget, timeout time.Duration) (daemonStamp, bool) {
	opts := t.Opts
	opts.StrictHostKey = true
	opts.OfferOneKey = true
	opts.Timeout = timeout
	resp, err := daemonrpc.SendRequestWith(t.Addr, daemonrpc.Request{Method: "version"}, opts)
	if err != nil || resp.Error != "" {
		return daemonStamp{}, false
	}
	var s daemonStamp
	if json.Unmarshal(resp.Result, &s) != nil || !s.usable() {
		return daemonStamp{}, false
	}
	return s, true
}

// mergeSameDaemon folds the records and rows of entries that are one daemon.
// Entries that gave rows are grouped by daemonKey; the members of a group of
// two or more report their pid and start time, and foldSameDaemon does the
// rest.
func mergeSameDaemon(targets []clusterTarget, records []clusterRecord, rowSets [][]api.Session, fingerprints []string) {
	keys := make([]string, len(targets))
	count := map[string]int{}
	for i, rec := range records {
		if rec.Outcome != outcomeAnswered && rec.Outcome != outcomeStarting {
			continue
		}
		keys[i] = daemonKey(targets[i].Addr, fingerprints[i])
		if keys[i] != "" {
			count[keys[i]]++
		}
	}
	stamps := make([]daemonStamp, len(targets))
	ok := make([]bool, len(targets))
	var wg sync.WaitGroup
	for i := range targets {
		if keys[i] == "" || count[keys[i]] < 2 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			stamps[i], ok[i] = readStamp(targets[i])
		}()
	}
	wg.Wait()
	foldSameDaemon(keys, stamps, ok, records, rowSets)
}

// foldSameDaemon merges the entries that share a non-empty key, given the
// stamp each reported. In a group, a member that agrees with the first member
// that reported a stamp reads same_daemon and names it. The rows shown are the
// union: a merged entry's row is kept only when no earlier row has its key, so
// nothing is dropped that a read saw and the other missed.
func foldSameDaemon(keys []string, stamps []daemonStamp, ok []bool, records []clusterRecord, rowSets [][]api.Session) {
	groups := map[string][]int{}
	var order []string
	for i, k := range keys {
		if k == "" {
			continue
		}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}
	for _, k := range order {
		lead := -1
		// have holds every key shown so far in this group: the leader's rows,
		// then each merged member's kept rows, so a row two later members both
		// saw is kept once (#1084).
		var have map[string]bool
		for _, i := range groups[k] {
			if !ok[i] {
				continue
			}
			if lead < 0 {
				lead = i
				have = map[string]bool{}
				for _, r := range rowSets[lead] {
					have[r.Workspace+"/"+r.Name] = true
				}
				continue
			}
			if stamps[i].PID != stamps[lead].PID || !stamps[i].StartedAt.Equal(stamps[lead].StartedAt) {
				continue
			}
			var kept []api.Session
			for _, r := range rowSets[i] {
				if !have[r.Workspace+"/"+r.Name] {
					kept = append(kept, r)
					have[r.Workspace+"/"+r.Name] = true
				}
			}
			rowSets[i] = kept
			n := len(kept)
			records[i].Outcome = outcomeSameDaemon
			records[i].SameDaemonAs = records[lead].Cluster
			records[i].Sessions = &n
			records[i].Cause = "same daemon as " + records[lead].Cluster
			records[i].Next = ""
		}
	}
}
