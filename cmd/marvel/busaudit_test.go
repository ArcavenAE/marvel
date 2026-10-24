package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// auditFakeDaemon answers every request with reply's response and records
// the requests. The socket lives in a short directory: a unix socket path is
// capped near 104 bytes.
func auditFakeDaemon(t *testing.T, reply func(daemonrpc.Request) daemonrpc.Response) (socket string, requests func() []daemonrpc.Request) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ba")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket = filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var (
		mu  sync.Mutex
		got []daemonrpc.Request
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemonrpc.Request
			if json.NewDecoder(c).Decode(&req) == nil {
				mu.Lock()
				got = append(got, req)
				mu.Unlock()
				_ = json.NewEncoder(c).Encode(reply(req))
			}
			_ = c.Close()
		}
	}()
	return socket, func() []daemonrpc.Request {
		mu.Lock()
		defer mu.Unlock()
		return append([]daemonrpc.Request(nil), got...)
	}
}

func recordReply(t *testing.T, rec bus.AuditRecord) func(daemonrpc.Request) daemonrpc.Response {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return func(daemonrpc.Request) daemonrpc.Response { return daemonrpc.Response{Result: data} }
}

func runBusAudit(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(append([]string{"bus", "audit"}, args...))
	code = execute(root, &errOut)
	return code, out.String(), errOut.String()
}

func refusedRecord() bus.AuditRecord {
	return bus.AuditRecord{
		Seq: 7, Time: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Subject: "agent.audit",
		Outcome: "refused", Stage: "scope", Refusal: "not permitted", MsgID: "abc-refused",
		Body: []byte(`{"text":"hello"}`),
	}
}

func TestBusAuditRefusesABadSeqBeforeAnyDial(t *testing.T) {
	resolveFixture(t, false)
	dir, err := os.MkdirTemp("", "bd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dead := filepath.Join(dir, "dead.sock")
	for _, args := range [][]string{{"abc"}, {"0"}, {"--", "-1"}, {"1.5"}, {"18446744073709551616"}, {}} {
		code, _, stderr := runBusAudit(t, append(args, "--socket", dead)...)
		if code != 1 {
			t.Errorf("%v: exit = %d, want 1", args, code)
		}
		if !strings.Contains(stderr, "whole number of 1 or more") {
			t.Errorf("%v: stderr = %q, want the argument text", args, stderr)
		}
		if strings.Contains(stderr, dead) {
			t.Errorf("%v: stderr names the socket, so it dialled: %q", args, stderr)
		}
	}
	// A bare -1 reads as a flag to the parser; it is still refused before any dial.
	code, _, stderr := runBusAudit(t, "-1", "--socket", dead)
	if code != 1 || strings.Contains(stderr, dead) || !strings.Contains(stderr, "-1") {
		t.Errorf("-1: exit = %d, stderr = %q, want exit 1 naming -1 and no dial", code, stderr)
	}
}

func TestBusAuditPrintsARefusedRecord(t *testing.T) {
	resolveFixture(t, false)
	socket, requests := auditFakeDaemon(t, recordReply(t, refusedRecord()))
	code, stdout, stderr := runBusAudit(t, "7", "--socket", socket)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	want := `seq:      7
time:     2026-10-20T12:00:00Z
subject:  agent.audit
outcome:  refused
stage:    scope
refusal:  "not permitted"
msg_id:   "abc-refused"
envelope:
  {
    "text": "hello"
  }
`
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	reqs := requests()
	if len(reqs) != 1 || reqs[0].Method != "bus.audit" || string(reqs[0].Params) != `{"seq":7}` {
		t.Errorf("requests = %+v, want one bus.audit with seq 7", reqs)
	}
}

func TestBusAuditSentRecordHasNoStageOrRefusal(t *testing.T) {
	resolveFixture(t, false)
	rec := refusedRecord()
	rec.Outcome, rec.Stage, rec.Refusal = "", "", ""
	var out bytes.Buffer
	if err := printBusAudit(&out, rec, false); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "outcome:  sent\n") {
		t.Errorf("no sent outcome:\n%s", text)
	}
	if strings.Contains(text, "stage:") || strings.Contains(text, "refusal:") {
		t.Errorf("a sent record prints stage or refusal:\n%s", text)
	}
}

func TestBusAuditJSONOutput(t *testing.T) {
	resolveFixture(t, false)
	socket, _ := auditFakeDaemon(t, recordReply(t, refusedRecord()))
	code, stdout, stderr := runBusAudit(t, "7", "-o", "json", "--socket", socket)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	var got struct {
		Seq      uint64          `json:"seq"`
		Time     string          `json:"time"`
		Outcome  string          `json:"outcome"`
		Stage    string          `json:"stage"`
		Refusal  string          `json:"refusal"`
		MsgID    string          `json:"msg_id"`
		Envelope json.RawMessage `json:"envelope"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", err, stdout)
	}
	if got.Seq != 7 || got.Outcome != "refused" || got.Stage != "scope" || got.Refusal != "not permitted" || got.MsgID != "abc-refused" {
		t.Errorf("fields = %+v", got)
	}
	if !json.Valid(got.Envelope) || !strings.Contains(string(got.Envelope), `"hello"`) {
		t.Errorf("envelope = %s", got.Envelope)
	}
}

func TestBusAuditRefusesAnUnknownOutputFormat(t *testing.T) {
	resolveFixture(t, false)
	socket, requests := auditFakeDaemon(t, recordReply(t, refusedRecord()))
	code, _, stderr := runBusAudit(t, "7", "-o", "yaml", "--socket", socket)
	if code != 1 || !strings.Contains(stderr, "json") {
		t.Errorf("exit = %d, stderr = %q, want a refusal that names json", code, stderr)
	}
	if n := len(requests()); n != 0 {
		t.Errorf("%d requests sent for a refused format", n)
	}
}

func TestBusAuditDaemonErrorExitsOneOnOneLine(t *testing.T) {
	resolveFixture(t, false)
	socket, _ := auditFakeDaemon(t, func(daemonrpc.Request) daemonrpc.Response {
		return daemonrpc.Response{Error: "read AGENT_AUDIT: boom \x1b[2J \u202e\nsecond"}
	})
	code, stdout, stderr := runBusAudit(t, "7", "--socket", socket)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n"); len(lines) != 1 {
		t.Errorf("stderr is %d lines, want one:\n%s", len(lines), stderr)
	}
	if !strings.Contains(stderr, "boom") {
		t.Errorf("stderr lost the daemon's text: %q", stderr)
	}
	for _, raw := range []string{"\x1b", "\u202e"} {
		if strings.Contains(stderr, raw) {
			t.Errorf("stderr holds a raw %U", []rune(raw)[0])
		}
	}
}

func TestBusAuditClusterFlagGoesThroughSend(t *testing.T) {
	resolveFixture(t, false)
	// The root command records that --cluster was given in a package variable.
	oldGiven := clusterFlagGiven
	t.Cleanup(func() { clusterFlagGiven = oldGiven })
	code, _, stderr := runBusAudit(t, "1", "--cluster", "")
	if code != 1 || !strings.Contains(stderr, "--cluster needs a cluster name") {
		t.Errorf("exit = %d, stderr = %q, want the shared --cluster refusal", code, stderr)
	}
}

func TestBusAuditHelpSaysWhatARecordHolds(t *testing.T) {
	var audit string
	for _, c := range busCmd().Commands() {
		if c.Name() == "audit" {
			audit = c.Long
		}
	}
	if !strings.Contains(audit, "full envelope") || !strings.Contains(audit, "any admin caller") {
		t.Errorf("help does not warn that a record holds the full envelope and any admin caller can read it:\n%s", audit)
	}
}
