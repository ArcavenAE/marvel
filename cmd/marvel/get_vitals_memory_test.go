package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/hostvitals"
)

// The top list names the seat a process belongs to, and the orphan list shows
// each orphan's RSS beside its CPU.
func TestPrintVitalsShowsSeatAndOrphanRSS(t *testing.T) {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	snap := hostvitals.Snapshot{
		Enabled: true, Sampled: true, At: &at,
		Top: []hostvitals.Proc{{PID: 101, PPID: 100, RSSBytes: 6 << 30, Name: "2.1.322", Seat: "ws/seat-a"}},
		Orphans: []hostvitals.Orphan{{
			PID: 301, Name: "modeld", Reason: hostvitals.ReasonSeatTree, Session: "ws/seat-a",
			MarkedAt: at, RSSBytes: 3 << 30,
		}},
	}
	var out bytes.Buffer
	if err := printVitals(&out, snap, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	top, orphans, _ := strings.Cut(text, "orphans:")
	if !strings.Contains(top, "SEAT") || !strings.Contains(top, "ws/seat-a") {
		t.Errorf("top list lacks a SEAT column naming ws/seat-a:\n%s", text)
	}
	if !strings.Contains(orphans, "RSS") || !strings.Contains(orphans, "3.0 GiB") {
		t.Errorf("orphan list lacks an RSS column with 3.0 GiB:\n%s", text)
	}
}
