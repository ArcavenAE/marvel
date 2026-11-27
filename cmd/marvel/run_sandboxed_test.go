package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

func TestRunSandboxedSendsTheRequestAndReturnsTheStatus(t *testing.T) {
	var got api.RunSandboxedRequest
	send := func(r daemonrpc.Request) (*daemonrpc.Response, error) {
		if r.Method != daemonrpc.MethodRunSandboxed {
			t.Errorf("method = %q", r.Method)
		}
		if err := json.Unmarshal(r.Params, &got); err != nil {
			t.Fatal(err)
		}
		res, _ := json.Marshal(api.RunSandboxedResult{ExitCode: 4, Stdout: "o", Stderr: "e", Truncated: true})
		return &daemonrpc.Response{Result: res}, nil
	}
	var out, errw bytes.Buffer
	rc, err := runSandboxed(&out, &errw, "tok", []string{"/usr/bin/true", "a"}, "/work",
		[]string{"A=1", api.HeartbeatTokenEnv + "=tok", api.HeartbeatTokenFileEnv + "=/f", "B=2"}, "(deny network*)\n", 30, send)
	if err != nil || rc != 4 {
		t.Fatalf("rc=%d err=%v, want 4 and nil", rc, err)
	}
	if got.Token != "tok" || got.Dir != "/work" || got.TimeoutSeconds != 30 || strings.Join(got.Argv, " ") != "/usr/bin/true a" {
		t.Fatalf("request = %+v", got)
	}
	if strings.Join(got.Env, ",") != "A=1,B=2" {
		t.Fatalf("env = %q, want the caller's environment without the heartbeat token", got.Env)
	}
	if out.String() != "o" || !strings.HasPrefix(errw.String(), "e") || !strings.Contains(errw.String(), "cut at") {
		t.Fatalf("out=%q err=%q", out.String(), errw.String())
	}
}

func TestRunSandboxedRefusesWithoutATokenAndSurfacesDaemonRefusals(t *testing.T) {
	never := func(daemonrpc.Request) (*daemonrpc.Response, error) {
		t.Fatal("a request was sent with no token")
		return nil, nil
	}
	if _, err := runSandboxed(nil, nil, "", []string{"/x"}, "/", nil, "", 0, never); err == nil {
		t.Fatal("no token was accepted")
	}
	refuse := func(daemonrpc.Request) (*daemonrpc.Response, error) {
		return &daemonrpc.Response{Error: "run.sandboxed: run profile refused: x"}, nil
	}
	if _, err := runSandboxed(&bytes.Buffer{}, &bytes.Buffer{}, "tok", []string{"/x"}, "/", nil, "", 0, refuse); err == nil ||
		!strings.Contains(err.Error(), "run profile refused") {
		t.Fatalf("err = %v, want the daemon's refusal", err)
	}
}
