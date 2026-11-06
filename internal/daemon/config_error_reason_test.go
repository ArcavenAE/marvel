package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// ADR-010 Amendment 2: a headless run that exited 78 reads with a
// configuration-error reason in both get sessions and describe session. The
// reason is a projection of the copies; the stored row keeps it empty.
func TestConfigErrorReasonShowsInGetAndDescribe(t *testing.T) {
	d := newHandlerDaemon(t)
	held := &api.Session{
		Name: "jobs-cfg-g1-0", Workspace: "cfgws", Team: "jobs", Role: "cfg", Generation: 1,
		State: api.SessionFailed, ExitStatus: "78",
		Runtime: api.Runtime{Name: "sh", Command: "sh", Mode: api.RuntimeModeHeadless},
	}
	other := &api.Session{
		Name: "jobs-bad-g1-0", Workspace: "cfgws", Team: "jobs", Role: "bad", Generation: 1,
		State: api.SessionFailed, ExitStatus: "1",
		Runtime: api.Runtime{Name: "sh", Command: "sh", Mode: api.RuntimeModeHeadless},
	}
	for _, s := range []*api.Session{held, other} {
		if err := d.store.CreateSession(s); err != nil {
			t.Fatalf("create %s: %v", s.Name, err)
		}
	}

	resp := d.handleGet(mustMarshal(t, map[string]string{"resource_type": "sessions"}))
	if resp.Error != "" {
		t.Fatalf("get: %s", resp.Error)
	}
	var rows []api.Session
	if err := json.Unmarshal(resp.Result, &rows); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Name] = true
		hasReason := strings.Contains(r.Reason, "configuration error")
		if r.Name == held.Name && !hasReason {
			t.Errorf("get sessions: exit-78 row reason = %q, want a configuration error", r.Reason)
		}
		if r.Name == other.Name && r.Reason != "" {
			t.Errorf("get sessions: exit-1 row reason = %q, want none", r.Reason)
		}
	}
	if !seen[held.Name] || !seen[other.Name] {
		t.Fatalf("get sessions returned %v, want both %s and %s", seen, held.Name, other.Name)
	}

	resp = d.handleDescribe(mustMarshal(t, map[string]string{"resource_type": "session", "name": held.Key()}))
	if resp.Error != "" || !strings.Contains(string(resp.Result), "configuration error") {
		t.Fatalf("describe session did not show the reason: %q %s", resp.Error, resp.Result)
	}
	resp = d.handleDescribe(mustMarshal(t, map[string]string{"resource_type": "session", "name": other.Key()}))
	if resp.Error != "" || strings.Contains(string(resp.Result), "configuration error") {
		t.Fatalf("describe of an exit-1 row shows a configuration error: %q %s", resp.Error, resp.Result)
	}

	stored, err := d.store.GetSession(held.Key())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Reason != "" {
		t.Fatalf("stored row carries projection text %q; Reason must stay empty in the store", stored.Reason)
	}
}
