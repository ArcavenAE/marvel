package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// fakeHibernate answers the verbs' RPCs in order and records every call.
type fakeHibernate struct {
	t      *testing.T
	calls  []string
	status []hibernateReply // successive status replies; the last repeats
	wake   wakeReply
	errs   map[string]string
}

func (f *fakeHibernate) call(req daemonrpc.Request) (*daemonrpc.Response, error) {
	var p map[string]any
	_ = json.Unmarshal(req.Params, &p)
	kind := req.Method
	switch {
	case p["dry_run"] == true:
		kind += " dry"
	case p["status"] == true:
		kind += " status"
	case p["end"] != nil:
		kind += " end"
	}
	f.calls = append(f.calls, kind)
	if msg, ok := f.errs[kind]; ok {
		return &daemonrpc.Response{Error: msg}, nil
	}
	var v any
	switch kind {
	case "team.hibernate dry":
		v = hibernateReply{Seats: 3, Labels: map[string]int{"bedrock": 2, "none": 1}}
	case "team.hibernate":
		v = hibernateReply{Teams: []hibernateTeamReply{{Team: "acme/squad", State: "hibernated"}}}
	case "team.hibernate status":
		r := f.status[0]
		if len(f.status) > 1 {
			f.status = f.status[1:]
		}
		v = r
	case "team.hibernate end":
		v = hibernateReply{Seats: 1}
	case "team.wake":
		v = f.wake
	}
	b, _ := json.Marshal(v)
	return &daemonrpc.Response{Result: b}, nil
}

func quick(t *testing.T) {
	t.Helper()
	oldPoll, oldWait := hibernatePoll, hibernateWait
	hibernatePoll, hibernateWait = time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { hibernatePoll, hibernateWait = oldPoll, oldWait })
}

func TestHibernateRefusesWithoutATerminalOrYes(t *testing.T) {
	quick(t)
	f := &fakeHibernate{t: t}
	var out bytes.Buffer
	code, err := runHibernate(&out, strings.NewReader(""), false, false, false, "", map[string]any{"all": true}, f.call)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("code %d err %v, want a refusal asking for a terminal or --yes", code, err)
	}
	for _, c := range f.calls {
		if c == "team.hibernate" {
			t.Fatal("a refused hibernate still started")
		}
	}
}

func TestHibernateConfirmPromptShowsSeatsPerLabel(t *testing.T) {
	quick(t)
	f := &fakeHibernate{t: t}
	var out bytes.Buffer
	code, err := runHibernate(&out, strings.NewReader("n\n"), true, false, false, "", map[string]any{"all": true}, f.call)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("code %d err %v, want not confirmed", code, err)
	}
	if !strings.Contains(out.String(), "3 seats (bedrock 2, none 1)") || !strings.Contains(out.String(), "[y/N]") {
		t.Fatalf("prompt = %q, want the seat count per label, none included, and [y/N]", out.String())
	}
}

func TestHibernateExitCodes(t *testing.T) {
	quick(t)
	done := hibernateReply{Teams: []hibernateTeamReply{{Team: "acme/squad", Done: true}}}
	f := &fakeHibernate{t: t, status: []hibernateReply{
		{Teams: []hibernateTeamReply{{Team: "acme/squad", Running: 1, Pending: []string{"acme/a"}}}}, done,
	}}
	var out bytes.Buffer
	if code, err := runHibernate(&out, strings.NewReader(""), false, true, false, "", map[string]any{"all": true}, f.call); code != 0 || err != nil {
		t.Fatalf("settled: code %d err %v out %q", code, err, out.String())
	}

	reported := hibernateReply{Teams: []hibernateTeamReply{{Team: "acme/squad", Running: 1, Reported: []string{"acme/a"}}}}
	f = &fakeHibernate{t: t, status: []hibernateReply{reported}}
	out.Reset()
	code, err := runHibernate(&out, strings.NewReader(""), false, true, false, "", map[string]any{"all": true}, f.call)
	if code != 3 || err != nil || !strings.Contains(out.String(), "acme/a") || !strings.Contains(out.String(), "hibernate --end") {
		t.Fatalf("reported seat: code %d err %v out %q, want 3 naming the seat and --end", code, err, out.String())
	}

	f = &fakeHibernate{t: t, errs: map[string]string{"team.hibernate": "hibernate refused: the cluster is suspended"}}
	if code, err := runHibernate(&out, strings.NewReader(""), false, true, false, "", map[string]any{"all": true}, f.call); code != 1 || err == nil {
		t.Fatalf("daemon refusal: code %d err %v, want 1", code, err)
	}
}

func TestHibernateEndAndNoWait(t *testing.T) {
	quick(t)
	f := &fakeHibernate{t: t}
	var out bytes.Buffer
	if code, err := runHibernate(&out, strings.NewReader(""), false, false, false, "acme/s1", nil, f.call); code != 0 || err != nil || len(f.calls) != 1 || f.calls[0] != "team.hibernate end" {
		t.Fatalf("--end: code %d err %v calls %v, want one end call and no confirm", code, err, f.calls)
	}
	f = &fakeHibernate{t: t}
	if code, err := runHibernate(&out, strings.NewReader(""), false, true, true, "", map[string]any{"all": true}, f.call); code != 0 || err != nil {
		t.Fatalf("--no-wait: code %d err %v", code, err)
	}
	for _, c := range f.calls {
		if c == "team.hibernate status" {
			t.Fatal("--no-wait polled")
		}
	}
}

func TestWakeReportsChecksAndWaitsForTheMarks(t *testing.T) {
	quick(t)
	f := &fakeHibernate{t: t, status: []hibernateReply{{Teams: []hibernateTeamReply{{Team: "acme/squad", State: "waking"}}}, {}}}
	f.wake.Checks = []struct {
		Name    string `json:"name"`
		Result  string `json:"result"`
		Detail  string `json:"detail"`
		Skipped bool   `json:"skipped"`
	}{{Name: "bus", Result: "passed", Detail: "no link declared"}, {Name: "harness-binary", Result: "skipped", Skipped: true}}
	f.wake.Teams = []hibernateTeamReply{{Team: "acme/squad", State: "waking"}}
	var out bytes.Buffer
	code, err := runWake(&out, strings.NewReader(""), false, true, map[string]any{"all": true}, f.call)
	if code != 0 || err != nil || !strings.Contains(out.String(), "check bus: passed") || !strings.Contains(out.String(), "check harness-binary: skipped") || !strings.Contains(out.String(), "woken: acme/squad") {
		t.Fatalf("code %d err %v out %q", code, err, out.String())
	}

	stuck := &fakeHibernate{t: t, status: []hibernateReply{{Teams: []hibernateTeamReply{{Team: "acme/squad", State: "waking"}}}}}
	stuck.wake.Teams = f.wake.Teams
	out.Reset()
	if code, _ := runWake(&out, strings.NewReader(""), false, true, map[string]any{"all": true}, stuck.call); code != 3 || !strings.Contains(out.String(), "still waking") {
		t.Fatalf("a team still waking: code %d out %q, want 3", code, out.String())
	}
}

func TestWakeRefusesWithoutATerminalOrYes(t *testing.T) {
	f := &fakeHibernate{t: t}
	var out bytes.Buffer
	if code, err := runWake(&out, strings.NewReader(""), false, false, map[string]any{"all": true}, f.call); code != 1 || err == nil || len(f.calls) != 0 {
		t.Fatalf("code %d err %v calls %v, want a refusal before any call", code, err, f.calls)
	}
}

func TestStatusListsHibernatedTeamsAndIgnoresAnOldDaemon(t *testing.T) {
	f := &fakeHibernate{t: t, status: []hibernateReply{{Teams: []hibernateTeamReply{{Team: "acme/squad", State: "hibernated", Done: true}}}}}
	var out bytes.Buffer
	reportHibernated(&out, f.call)
	if out.String() != "team acme/squad: hibernated\n" {
		t.Fatalf("status lines = %q", out.String())
	}
	old := &fakeHibernate{t: t, errs: map[string]string{"team.hibernate status": "unknown method team.hibernate"}}
	out.Reset()
	reportHibernated(&out, old.call)
	if out.Len() != 0 {
		t.Fatalf("an old daemon added status lines: %q", out.String())
	}
}

func TestScaleAndApplyPrintTheHoldLineOnAHibernatedTeam(t *testing.T) {
	got := scaledLines("acme/squad", "worker", 2, json.RawMessage(`{"held":"held: hibernated; run marvel wake"}`))
	if !strings.Contains(got, "scaled to 2 replicas\nheld: hibernated; run marvel wake\n") {
		t.Fatalf("scale output = %q, want the hold line after the change", got)
	}
	if got := scaledLines("acme/squad", "worker", 2, json.RawMessage(`{}`)); strings.Contains(got, "held") {
		t.Fatalf("scale output on a running team = %q, want no hold line", got)
	}
	if got := heldLines([]string{"acme/squad"}, "held: hibernated; run marvel wake"); got != "team/acme/squad held: hibernated; run marvel wake\n" {
		t.Fatalf("apply output = %q", got)
	}
}

func TestHibernateHelpSaysSeatsReturnAsSuccessors(t *testing.T) {
	long := hibernateCmd().Long
	if !strings.Contains(long, "Seats return as successors that read their handoff") {
		t.Fatalf("hibernate help = %q, want the successor sentence", long)
	}
}

func TestHeldLineSaysSeatsReturnAsSuccessors(t *testing.T) {
	if !strings.Contains(api.HibernateHeldLine, "seats return as successors reading their handoff") {
		t.Fatalf("held line = %q, want the successor clause", api.HibernateHeldLine)
	}
	if !strings.HasPrefix(api.HibernateHeldLine, "held: hibernated; run marvel wake") {
		t.Fatalf("held line = %q, want it to keep the wake pointer", api.HibernateHeldLine)
	}
}
