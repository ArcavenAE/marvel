package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookAlwaysExitsZeroAndPrintsNothing(t *testing.T) {
	var out, errb bytes.Buffer
	// An unwritable log must not change what claude sees.
	code := run([]string{"hook", "--log", filepath.Join(t.TempDir(), "no", "such", "dir", "e.tsv")}, strings.NewReader(`{"hook_event_name":"Stop"}`), &out, &errb)
	if code != 0 || out.Len() != 0 {
		t.Errorf("hook exit %d, stdout %q; want 0 and nothing", code, out.String())
	}
	if run([]string{"hook"}, strings.NewReader(""), &out, &errb) != 0 || out.Len() != 0 {
		t.Error("hook with no --log must still exit zero and print nothing")
	}
}

func TestMarkThenCheckWritesTheResultByRename(t *testing.T) {
	dir := t.TempDir()
	log, res := filepath.Join(dir, "events.tsv"), filepath.Join(dir, "probe_result.json")
	var out, errb bytes.Buffer
	for _, args := range [][]string{
		{"mark", "--log", log, "stdout-canary", "yes"},
		{"check", "--log", log, "--out", res, "--stamp", "claude 2.1.296"},
	} {
		if code := run(args, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("%v exited %d: %s", args, code, errb.String())
		}
	}
	b, err := os.ReadFile(res)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		t.Fatalf("not json: %s", b)
	}
	status := func(key string) string {
		var c struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(raw[key], &c)
		return c.Status
	}
	if string(raw["build_stamp"]) != `"claude 2.1.296"` || status("c9") != "observed" || status("c1") != "not-run" {
		t.Errorf("result = %s", b)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".probe_result-*"))
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestCheckRefusesATornLog(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "events.tsv")
	if err := os.WriteFile(log, []byte("12\thook\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"check", "--log", log, "--out", filepath.Join(dir, "r.json"), "--stamp", "s"}, strings.NewReader(""), &out, &errb); code == 0 {
		t.Error("check wrote a result from a malformed log")
	}
	if _, err := os.Stat(filepath.Join(dir, "r.json")); err == nil {
		t.Error("a result file exists for a malformed log")
	}
}
