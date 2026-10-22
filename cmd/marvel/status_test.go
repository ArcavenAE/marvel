package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/suspend"
)

func suspendedStatusRecord() suspend.Record {
	return suspend.Record{
		State: suspend.StateSuspended, Actor: "op", Source: "cli",
		Since:  time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC),
		Server: suspend.Proc{Pid: 100, Identity: childproof.Identity{Start: "linux:b:1"}},
	}
}

func statusAnswer(t *testing.T, st suspend.Status) *daemonrpc.Response {
	t.Helper()
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	return &daemonrpc.Response{Result: b}
}

func TestReportStatusFromTheDaemon(t *testing.T) {
	now := time.Date(2026, 10, 20, 22, 30, 0, 0, time.UTC)
	var out bytes.Buffer
	code, err := reportStatus(&out, "/run/marvel.sock", statusAnswer(t, suspend.StatusOf(nil)), nil, now)
	if err != nil || code != 0 || out.String() != "cluster: running\n" {
		t.Errorf("running: code %d err %v out %q", code, err, out.String())
	}
	out.Reset()
	rec := suspendedStatusRecord()
	code, err = reportStatus(&out, "/run/marvel.sock", statusAnswer(t, suspend.StatusOf(&rec)), nil, now)
	if err != nil || code != 3 || !strings.Contains(out.String(), "cluster: suspended since") || strings.Contains(out.String(), "not answering") {
		t.Errorf("suspended: code %d err %v out %q", code, err, out.String())
	}
}

// With the daemon down, the mirror beside the socket answers and says so; with
// no mirror, or a TCP address, status cannot say and exits 1.
func TestReportStatusFromTheMirror(t *testing.T) {
	now := time.Date(2026, 10, 20, 22, 30, 0, 0, time.UTC)
	down := errors.New("connection refused")
	sock := filepath.Join(t.TempDir(), "marvel.sock")

	var out bytes.Buffer
	if code, err := reportStatus(&out, sock, nil, down, now); code != 1 || err == nil || !strings.Contains(err.Error(), "no suspend mirror") {
		t.Errorf("no mirror: code %d err %v", code, err)
	}
	if err := suspend.WriteMirror(suspend.MirrorPath(sock), suspendedStatusRecord()); err != nil {
		t.Fatal(err)
	}
	code, err := reportStatus(&out, sock, nil, down, now)
	if err != nil || code != 3 || !strings.Contains(out.String(), "daemon not answering") || !strings.Contains(out.String(), "kill -CONT 100") {
		t.Errorf("mirror: code %d err %v out %q", code, err, out.String())
	}
	if code, err := reportStatus(&out, "127.0.0.1:7000", nil, down, now); code != 1 || err == nil || !strings.Contains(err.Error(), "TCP") {
		t.Errorf("tcp: code %d err %v", code, err)
	}
}

func TestReportStatusFromAnOlderDaemon(t *testing.T) {
	var out bytes.Buffer
	code, err := reportStatus(&out, "/run/marvel.sock", &daemonrpc.Response{Error: "unknown method: suspend.status"}, nil, time.Now())
	if code != 1 || err == nil || !strings.Contains(err.Error(), "predates") {
		t.Errorf("older daemon: code %d err %v", code, err)
	}
}

// The daemon's reason for an unsettled record is text from a stored record, so
// it is drawn through the same filter as every other daemon string.
func TestReportStatusFiltersTheUnsettledReason(t *testing.T) {
	st := suspend.Status{State: suspend.StateUnknown, Unsettled: "parse the stored record: \x1b[31mbad‮"}
	var out bytes.Buffer
	if _, err := reportStatus(&out, "/run/marvel.sock", statusAnswer(t, st), nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	drawnClean(t, "unsettled reason", out.String())
}
