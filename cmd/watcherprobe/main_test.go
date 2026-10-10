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

func TestMaskHidesKeyShapesAndFragmentsOfTheSecret(t *testing.T) {
	const secret = "sk-ant-api03-FAKEFAKEFAKE0123456789abcdefXYZ"
	t.Setenv("WATCHER_PROBE_MASK", secret)
	in := strings.Join([]string{
		"full " + secret,
		"shape sk-ant-api03-OTHERKEYTEXT99",
		"ellipsis sk-ant-...0123456789abcdefXYZ",
		"tail 0123456789abcdefXYZ",
		"short 0123456 stays",
		"task-queue stays and so does sk",
	}, "\n") + "\n"
	var out, errb bytes.Buffer
	if code := run([]string{"mask"}, strings.NewReader(in), &out, &errb); code != 0 {
		t.Fatalf("mask exited %d: %s", code, errb.String())
	}
	got := out.String()
	for _, leak := range []string{"FAKEFAKE", "OTHERKEY", "sk-ant-", "0123456789", "XYZ"} {
		if strings.Contains(got, leak) {
			t.Errorf("mask left %q in:\n%s", leak, got)
		}
	}
	for _, keep := range []string{"short 0123456 stays", "task-queue stays and so does sk"} {
		if !strings.Contains(got, keep) {
			t.Errorf("mask changed ordinary text %q:\n%s", keep, got)
		}
	}
	if strings.Count(got, "\n") != strings.Count(in, "\n") {
		t.Errorf("mask changed the line count:\n%s", got)
	}
}

func TestMaskWithNoSecretStillHidesKeyShapes(t *testing.T) {
	t.Setenv("WATCHER_PROBE_MASK", "")
	var out, errb bytes.Buffer
	if code := run([]string{"mask"}, strings.NewReader("key sk-ant-api03-ABCDEFGH12345\n"), &out, &errb); code != 0 {
		t.Fatalf("mask exited %d: %s", code, errb.String())
	}
	if strings.Contains(out.String(), "ABCDEFGH") || !strings.HasPrefix(out.String(), "key ") {
		t.Errorf("mask output = %q; want the key shape hidden and the rest kept", out.String())
	}
}

// The credential can appear in a hook payload, a statusline payload or a
// Notification message, and c6 copies that message into the result. None of it
// may reach events.tsv or probe_result.json, whole or in part.
func TestAKeyInAHookStatuslineOrNotificationPayloadReachesNeitherFile(t *testing.T) {
	const key = "sk-ant-api03-FAKEFAKEFAKE0123456789abcdefXYZ"
	t.Setenv("WATCHER_PROBE_KEY", key)
	dir := t.TempDir()
	log, res := filepath.Join(dir, "events.tsv"), filepath.Join(dir, "probe_result.json")
	var out, errb bytes.Buffer
	do := func(stdin string, args ...string) {
		t.Helper()
		out.Reset()
		if code := run(args, strings.NewReader(stdin), &out, &errb); code != 0 {
			t.Fatalf("%v exited %d: %s", args, code, errb.String())
		}
	}
	do("", "mark", "--log", log, "denial-start")
	do(`{"hook_event_name":"UserPromptSubmit","prompt":"my key is `+key+`"}`, "hook", "--log", log)
	do(`{"hook_event_name":"Notification","message":"auth failed for sk-ant-...0123456789abcdefXYZ"}`, "hook", "--log", log)
	do(`{"hook_event_name":"PermissionDenied","message":"tail 0123456789abcdefXYZ was refused"}`, "hook", "--log", log)
	do(`{"cost":{"total_cost_usd":0.5},"note":"`+key+`"}`, "statusline", "--log", log)
	if strings.Contains(out.String(), "FAKEFAKE") {
		t.Errorf("the statusline printed key text: %q", out.String())
	}
	do("", "mark", "--log", log, "denial-end")
	do("", "check", "--log", log, "--out", res, "--stamp", "s")
	for _, path := range []string{log, res} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{"FAKEFAKE", "0123456789", "sk-ant-", "XYZ"} {
			if strings.Contains(string(b), leak) {
				t.Errorf("%s holds %q:\n%s", filepath.Base(path), leak, b)
			}
		}
	}
	b, _ := os.ReadFile(res)
	if !strings.Contains(string(b), "was refused") {
		t.Errorf("the denial text was lost along with the key:\n%s", b)
	}
}

// The result is masked on its own, so a log that holds key text (written by an
// older logger, or by hand) still cannot carry it into probe_result.json.
func TestCheckMasksTheResultEvenFromAnUnmaskedLog(t *testing.T) {
	const key = "sk-ant-api03-FAKEFAKEFAKE0123456789abcdefXYZ"
	t.Setenv("WATCHER_PROBE_KEY", key)
	dir := t.TempDir()
	log, res := filepath.Join(dir, "events.tsv"), filepath.Join(dir, "probe_result.json")
	lines := "1\tmark\tdenial-start\t\n" +
		"2\thook\tNotification\t{\"hook_event_name\":\"Notification\",\"message\":\"bad key " + key + "\"}\n" +
		"3\tmark\tdenial-end\t\n"
	if err := os.WriteFile(log, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"check", "--log", log, "--out", res, "--stamp", "s"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("check exited %d: %s", code, errb.String())
	}
	b, err := os.ReadFile(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "FAKEFAKE") || !strings.Contains(string(b), "bad key") {
		t.Errorf("result = %s", b)
	}
}
