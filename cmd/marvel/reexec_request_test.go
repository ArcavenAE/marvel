package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemon"
)

func withSocket(t *testing.T, addr string) {
	t.Helper()
	old := socketPath
	socketPath = addr
	t.Cleanup(func() { socketPath = old })
}

func TestReexecRequestNamesThisBinaryOnTheLocalSocket(t *testing.T) {
	withSocket(t, filepath.Join(t.TempDir(), "marvel.sock"))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	req, sent := reexecRequest()
	if req.Method != "reexec" || sent != exe {
		t.Fatalf("want a reexec naming %s, got %+v sent=%q", exe, req, sent)
	}
	var p map[string]string
	if err := json.Unmarshal(req.Params, &p); err != nil || p["exec_path"] != exe {
		t.Fatalf("params %s (%v): want exec_path %s", req.Params, err, exe)
	}
}

func TestReexecRequestSendsNoPathToARemoteDaemon(t *testing.T) {
	for _, addr := range []string{"mrvl://desk", "ssh://user@host/run/marvel.sock", "tcp://10.0.0.1:6785"} {
		withSocket(t, addr)
		req, sent := reexecRequest()
		if len(req.Params) != 0 || sent != "" {
			t.Errorf("%s: a path on this host means nothing there, got %s sent=%q", addr, req.Params, sent)
		}
	}
}

func TestReexecNoteSaysWhenAnOlderDaemonKeptItsOwnPath(t *testing.T) {
	resp := func(binary string) *daemon.Response {
		b, _ := json.Marshal(map[string]string{"status": "reexec", "binary": binary})
		return &daemon.Response{Result: b}
	}
	if got := reexecNote(resp("/new/marvel"), "/new/marvel"); got != "" {
		t.Errorf("the daemon exec'd the named binary, got note %q", got)
	}
	if got := reexecNote(resp("/old/marvel"), ""); got != "" {
		t.Errorf("no path was sent, got note %q", got)
	}
	got := reexecNote(resp("/old/marvel"), "/new/marvel")
	if !strings.Contains(got, "/old/marvel") || !strings.Contains(got, "/new/marvel") {
		t.Errorf("want a note naming both paths, got %q", got)
	}
}

// The four places that send an operator to reexec say what it does now.
func TestReexecPointersSayWhatItAdopts(t *testing.T) {
	if strings.Contains(adoptsText, "installed binary") {
		t.Fatalf("adoptsText still claims the installed binary: %q", adoptsText)
	}
	var out strings.Builder
	versionReport(&out, daemon.Build{Version: "0.3.0", Channel: "alpha"}, func() (*daemon.BuildInfo, error) {
		return nil, errDaemonPredates
	})
	if !strings.Contains(out.String(), adoptsText) {
		t.Errorf("the predates line: %q", out.String())
	}
	warning, _ := compareBuilds(daemon.Build{Version: "0.3.0", Channel: "alpha"}, daemon.Build{Version: "0.2.0", Channel: "alpha"})
	if !strings.Contains(warning, adoptsText) {
		t.Errorf("the different-build warning: %q", warning)
	}
	for _, f := range []string{"upgrade_after.go", "main.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "adopts the installed binary") || strings.Contains(string(b), "adopt a freshly installed binary") {
			t.Errorf("%s still carries the old claim", f)
		}
	}
}
