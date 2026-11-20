package daemon

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
)

// A cast with a task shows it in get and in events: `marvel run --task` names
// the bd ticket or the ask the session works on, the session row carries it,
// and the session's events carry it, stamped once by the event ring.
func TestRunWithATaskShowsItInGetAndInEvents(t *testing.T) {
	d := newHandlerDaemon(t)

	resp := d.handleRun(mustMarshal(t, daemonrpc.RunParams{
		Workspace: "taskrun", Team: "adhoc", Role: "adhoc", Name: "with-task", Task: "aae-orc-p0xrm",
		RuntimeCommand: "sleep", RuntimeArgs: []string{"300"},
	}))
	if resp.Error != "" {
		t.Fatalf("run: %s", resp.Error)
	}
	resp = d.handleRun(mustMarshal(t, daemonrpc.RunParams{
		Workspace: "taskrun", Team: "adhoc", Role: "adhoc", Name: "no-task",
		RuntimeCommand: "sleep", RuntimeArgs: []string{"300"},
	}))
	if resp.Error != "" {
		t.Fatalf("run without a task: %s", resp.Error)
	}

	got := d.handleGet(mustMarshal(t, map[string]string{"resource_type": "sessions"}))
	if got.Error != "" {
		t.Fatalf("get: %s", got.Error)
	}
	rows := string(got.Result)
	if !strings.Contains(rows, `"task":"aae-orc-p0xrm"`) {
		t.Errorf("get sessions does not show the task:\n%s", rows)
	}
	if n := strings.Count(rows, `"task"`); n != 1 {
		t.Errorf("get sessions has %d task keys, want 1 (the session with no task omits it):\n%s", n, rows)
	}

	var withTask, other int
	for _, ev := range d.events.Snapshot(events.Filter{Kind: events.KindSessionCreated}, 0) {
		switch {
		case ev.Session == "taskrun/with-task" && ev.Task == "aae-orc-p0xrm":
			withTask++
		case ev.Session == "taskrun/with-task", ev.Task != "":
			other++
		}
	}
	if withTask != 1 || other != 0 {
		t.Errorf("session.created events: %d carry the task, %d are wrong, want 1 and 0", withTask, other)
	}
}

// A task that is not a key is refused before anything starts.
func TestRunRefusesATaskThatIsNotAKey(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.handleRun(mustMarshal(t, daemonrpc.RunParams{
		Workspace: "taskrun", Name: "bad-task", Task: "two words",
		RuntimeCommand: "sleep", RuntimeArgs: []string{"300"},
	}))
	if resp.Error == "" || !strings.Contains(resp.Error, "task") {
		t.Fatalf("run error = %q, want a refusal that names the task", resp.Error)
	}
	if rows := d.store.ListSessionsByTeam("taskrun", "adhoc"); len(rows) != 0 {
		t.Errorf("a refused run left %d session rows", len(rows))
	}
}
