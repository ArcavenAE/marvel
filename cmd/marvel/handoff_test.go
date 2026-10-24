package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/team"
)

func okResult(t *testing.T, path string, n int) *daemonrpc.Response {
	t.Helper()
	b, err := json.Marshal(api.HandoffPutResult{Path: path, Bytes: n})
	if err != nil {
		t.Fatal(err)
	}
	return &daemonrpc.Response{Result: b}
}

func TestHandoffPutSendsTheTokenAndTheText(t *testing.T) {
	var got daemonrpc.Request
	send := func(r daemonrpc.Request) (*daemonrpc.Response, error) {
		got = r
		return okResult(t, "/h/x.md", 12), nil
	}
	var out bytes.Buffer
	if err := handoffPut(strings.NewReader("my handoff\n"), &out, "tok-1", send); err != nil {
		t.Fatalf("handoffPut: %v", err)
	}
	if got.Method != "handoff.put" {
		t.Fatalf("method = %q, want handoff.put", got.Method)
	}
	var p api.HandoffPutRequest
	if err := json.Unmarshal(got.Params, &p); err != nil || p.Token != "tok-1" || p.Text != "my handoff\n" {
		t.Fatalf("params = %s (%v), want the token and the text", got.Params, err)
	}
	if !strings.Contains(out.String(), "/h/x.md") {
		t.Fatalf("output = %q, want the path the daemon wrote", out.String())
	}
}

func TestHandoffPutNeedsAToken(t *testing.T) {
	called := false
	send := func(daemonrpc.Request) (*daemonrpc.Response, error) { called = true; return nil, nil }
	err := handoffPut(strings.NewReader("x"), &bytes.Buffer{}, "", send)
	if err == nil || !strings.Contains(err.Error(), api.HeartbeatTokenEnv) {
		t.Fatalf("err = %v, want it to name %s", err, api.HeartbeatTokenEnv)
	}
	if called {
		t.Fatal("a request went out with no token")
	}
}

func TestHandoffPutRefusesOversizeInputBeforeSending(t *testing.T) {
	called := false
	send := func(daemonrpc.Request) (*daemonrpc.Response, error) { called = true; return nil, nil }
	big := strings.NewReader(strings.Repeat("x", team.MaxHandoffPutBytes+1))
	err := handoffPut(big, &bytes.Buffer{}, "tok", send)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want a too large refusal", err)
	}
	if called {
		t.Fatal("an oversize handoff was sent")
	}
}

func TestHandoffPutRefusesEmptyInput(t *testing.T) {
	send := func(daemonrpc.Request) (*daemonrpc.Response, error) { return nil, errors.New("must not send") }
	err := handoffPut(strings.NewReader("  \n"), &bytes.Buffer{}, "tok", send)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want an empty-input refusal before any send", err)
	}
}

func TestHandoffPutSurfacesTheDaemonsRefusal(t *testing.T) {
	send := func(daemonrpc.Request) (*daemonrpc.Response, error) {
		return &daemonrpc.Response{Error: "handoff.put: this session holds no pending handoff request"}, nil
	}
	err := handoffPut(strings.NewReader("x"), &bytes.Buffer{}, "tok", send)
	if err == nil || !strings.Contains(err.Error(), "no pending handoff request") {
		t.Fatalf("err = %v, want the daemon's reason", err)
	}
}
