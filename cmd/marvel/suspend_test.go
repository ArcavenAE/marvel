package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// verbDaemon answers get sessions with n sessions and records every method.
type verbDaemon struct {
	seats   int
	methods []string
	answer  map[string]*daemonrpc.Response
	err     error
}

func (f *verbDaemon) call(req daemonrpc.Request) (*daemonrpc.Response, error) {
	f.methods = append(f.methods, req.Method)
	if f.err != nil {
		return nil, f.err
	}
	if r, ok := f.answer[req.Method]; ok {
		return r, nil
	}
	if req.Method == "get" {
		rows := make([]map[string]string, f.seats)
		for i := range rows {
			rows[i] = map[string]string{"name": "s"}
		}
		b, _ := json.Marshal(rows)
		return &daemonrpc.Response{Result: b}, nil
	}
	return &daemonrpc.Response{Result: json.RawMessage(`{"suspended":true,"seats":4}`)}, nil
}

// On a terminal, suspend states what it freezes and how many seats, asks, and
// sends nothing on anything but a yes.
func TestSuspendAsksOnATerminal(t *testing.T) {
	for answer, wantSent := range map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, "YES\n": true, "Yes\n": true, " y \n": true,
		"\n": false, "n\n": false, "": false, "yep\n": false, "ye\n": false, "no\n": false, "y n\n": false,
	} {
		f := &verbDaemon{seats: 3}
		var out bytes.Buffer
		code, err := runSuspend(&out, strings.NewReader(answer), true, false, f.call)
		sent := false
		for _, m := range f.methods {
			if m == "suspend" {
				sent = true
			}
		}
		if sent != wantSent {
			t.Errorf("answer %q: suspend sent = %v, want %v (code %d err %v)", answer, sent, wantSent, code, err)
		}
		if !strings.Contains(out.String(), "3 seats") || !strings.Contains(out.String(), "[y/N]") {
			t.Errorf("answer %q: prompt = %q, want the seat count and [y/N]", answer, out.String())
		}
		if !wantSent && err == nil {
			t.Errorf("answer %q: no error for a refused confirm", answer)
		}
	}
}

// With no terminal and no --yes, suspend refuses before it sends anything.
func TestSuspendWithoutATerminalNeedsYes(t *testing.T) {
	f := &verbDaemon{seats: 1}
	var out bytes.Buffer
	code, err := runSuspend(&out, strings.NewReader("y\n"), false, false, f.call)
	if err == nil || code != 1 || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("code %d err %v, want exit 1 naming --yes", code, err)
	}
	if len(f.methods) != 0 {
		t.Errorf("methods = %v, want none", f.methods)
	}
}

func TestSuspendWithYesSendsItsOwnPid(t *testing.T) {
	var got daemonrpc.Request
	f := &verbDaemon{answer: map[string]*daemonrpc.Response{}}
	call := func(req daemonrpc.Request) (*daemonrpc.Response, error) {
		if req.Method == "suspend" {
			got = req
		}
		return f.call(req)
	}
	var out bytes.Buffer
	code, err := runSuspend(&out, strings.NewReader(""), false, true, call)
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v", code, err)
	}
	var p struct {
		CLIPid int `json:"cli_pid"`
	}
	if err := json.Unmarshal(got.Params, &p); err != nil || p.CLIPid <= 1 {
		t.Errorf("params = %s, want the CLI's pid", got.Params)
	}
	if !strings.Contains(out.String(), "suspended") {
		t.Errorf("out = %q", out.String())
	}
}

// The daemon's refusal reaches the operator word for word, with exit 1.
func TestSuspendPassesTheDaemonsRefusalThrough(t *testing.T) {
	f := &verbDaemon{answer: map[string]*daemonrpc.Response{"suspend": {Error: "suspend: pid 201 did not stop (it did not read stopped); everything stopped was resumed and the record cleared"}}}
	var out bytes.Buffer
	code, err := runSuspend(&out, strings.NewReader(""), false, true, f.call)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "did not stop") {
		t.Errorf("code %d err %v", code, err)
	}
}

func TestSuspendReportsAnUnreachableDaemon(t *testing.T) {
	f := &verbDaemon{err: errors.New("connection refused")}
	var out bytes.Buffer
	if code, err := runSuspend(&out, strings.NewReader(""), false, true, f.call); code != 1 || err == nil {
		t.Errorf("code %d err %v, want exit 1", code, err)
	}
}

// Resume has no dialog. A clean resume exits 0; one that found a process gone
// or left one stopped lists them and exits 3.
func TestResumeExitCodes(t *testing.T) {
	cases := map[string]struct {
		result   string
		wantCode int
		wantOut  []string
	}{
		"clean":   {`{"resumed":true}`, 0, []string{"resumed"}},
		"partial": {`{"resumed":true,"gone":[200],"still_stopped":[201]}`, 3, []string{"200", "201", "gone", "still stopped"}},
	}
	for name, c := range cases {
		f := &verbDaemon{answer: map[string]*daemonrpc.Response{"resume": {Result: json.RawMessage(c.result)}}}
		var out bytes.Buffer
		code, err := runResume(&out, f.call)
		if code != c.wantCode {
			t.Errorf("%s: code %d err %v, want %d", name, code, err, c.wantCode)
		}
		for _, w := range c.wantOut {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: out %q lacks %q", name, out.String(), w)
			}
		}
		if len(f.methods) != 1 || f.methods[0] != "resume" {
			t.Errorf("%s: methods = %v, want only resume", name, f.methods)
		}
	}
}

func TestResumePassesTheDaemonsRefusalThrough(t *testing.T) {
	f := &verbDaemon{answer: map[string]*daemonrpc.Response{"resume": {Error: "resume: the cluster is not suspended"}}}
	var out bytes.Buffer
	if code, err := runResume(&out, f.call); code != 1 || err == nil || !strings.Contains(err.Error(), "not suspended") {
		t.Errorf("code %d err %v", code, err)
	}
}

// An old daemon that has no such method says so, not "unknown method".
func TestSuspendNamesAnOldDaemon(t *testing.T) {
	f := &verbDaemon{answer: map[string]*daemonrpc.Response{"suspend": {Error: "unknown method: suspend"}}}
	var out bytes.Buffer
	code, err := runSuspend(&out, strings.NewReader(""), false, true, f.call)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "predates") {
		t.Errorf("code %d err %v, want a predates-suspend message", code, err)
	}
}

// wake names the hibernate verb's return; resume must not borrow it.
func TestResumeHelpDoesNotCallItselfWake(t *testing.T) {
	c := resumeCmd()
	for name, text := range map[string]string{"Short": c.Short, "Long": c.Long} {
		if strings.Contains(strings.ToLower(text), "wake") {
			t.Fatalf("resume %s = %q, want no use of wake", name, text)
		}
	}
}

func TestSuspendPromptDoesNotCallResumeWake(t *testing.T) {
	var out bytes.Buffer
	f := &verbDaemon{seats: 2}
	_, _ = runSuspend(&out, strings.NewReader("n\n"), true, false, f.call)
	if !strings.Contains(out.String(), "Suspend the cluster?") {
		t.Fatalf("no prompt printed: %q", out.String())
	}
	if strings.Contains(strings.ToLower(out.String()), "wake") {
		t.Fatalf("suspend prompt = %q, want no use of wake", out.String())
	}
}
