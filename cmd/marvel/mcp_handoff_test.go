package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

const (
	mcpTestToken = "tok-SECRET-4711"
	mcpTestText  = "handoff-text-DISTINCT-9921"
)

// mcpFake is a fake daemon on a unix socket in a short temp dir. It counts
// connections and records every request; reply decides the answer, and a nil
// reply means the connection is held open and never answered.
type mcpFake struct {
	socket string
	conns  atomic.Int32
	mu     sync.Mutex
	reqs   []daemonrpc.Request
	reply  func(daemonrpc.Request) *daemonrpc.Response
}

func newMCPFake(t *testing.T) *mcpFake {
	t.Helper()
	dir, err := os.MkdirTemp("", "mh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &mcpFake{socket: filepath.Join(dir, "s.sock")}
	f.reply = func(daemonrpc.Request) *daemonrpc.Response {
		b, _ := json.Marshal(api.HandoffPutResult{Path: "/h/seat.md", Bytes: 17})
		return &daemonrpc.Response{Result: b}
	}
	ln, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.conns.Add(1)
			go func() {
				defer func() { _ = c.Close() }()
				var req daemonrpc.Request
				if json.NewDecoder(c).Decode(&req) != nil {
					return
				}
				f.mu.Lock()
				f.reqs = append(f.reqs, req)
				reply := f.reply
				f.mu.Unlock()
				resp := reply(req)
				if resp == nil {
					<-done
					return
				}
				_ = json.NewEncoder(c).Encode(resp)
			}()
		}
	}()
	return f
}

func (f *mcpFake) requests() []daemonrpc.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]daemonrpc.Request(nil), f.reqs...)
}

func mcpEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

type mcpRun struct {
	stdout, stderr string
	err            error
}

func (r mcpRun) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q (%v)", l, err)
		}
		if m["jsonrpc"] != "2.0" {
			t.Fatalf("stdout line is not JSON-RPC 2.0: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func runMCP(t *testing.T, env map[string]string, timeout time.Duration, input string) mcpRun {
	t.Helper()
	var so, se bytes.Buffer
	err := serveMCPHandoff(strings.NewReader(input), &so, &se, mcpHandoffOptions{Env: mcpEnv(env), Timeout: timeout, Version: "vtest"})
	return mcpRun{stdout: so.String(), stderr: se.String(), err: err}
}

const mcpInit = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}` + "\n" +
	`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"

func mcpCall(id int, args string) string {
	return `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"tools/call","params":{"name":"submit_handoff","arguments":` + args + `}}` + "\n"
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func mcpTextArg(t *testing.T, text string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// nthReply returns reply i (negative counts from the end) or fails the test, so a
// missing reply is an assertion and not a panic.
func nthReply(t *testing.T, ls []map[string]any, i int) map[string]any {
	t.Helper()
	if i < 0 {
		i += len(ls)
	}
	if i < 0 || i >= len(ls) {
		t.Fatalf("reply %d missing: %d replies", i, len(ls))
	}
	return ls[i]
}

func resultOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", m)
	}
	return r
}

func errCode(t *testing.T, m map[string]any) float64 {
	t.Helper()
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error in %v", m)
	}
	return e["code"].(float64)
}

// TestMCPHandoffInitializeNegotiatesVersion: a client version the server
// knows is returned, any other gets 2025-06-18, and the capabilities are
// exactly tools with listChanged false.
func TestMCPHandoffInitializeNegotiatesVersion(t *testing.T) {
	for _, tc := range []struct{ ask, want string }{
		{"2025-06-18", "2025-06-18"},
		{"2025-03-26", "2025-03-26"},
		{"2024-11-05", "2024-11-05"},
		{"2099-01-01", "2025-06-18"},
		{"", "2025-06-18"},
	} {
		in := `{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"protocolVersion":"` + tc.ask + `","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}` + "\n"
		run := runMCP(t, nil, time.Second, in)
		ls := run.lines(t)
		if len(ls) != 1 {
			t.Fatalf("ask %q: %d replies: %s", tc.ask, len(ls), run.stdout)
		}
		r := resultOf(t, nthReply(t, ls, 0))
		if r["protocolVersion"] != tc.want {
			t.Errorf("ask %q: version = %v, want %s", tc.ask, r["protocolVersion"], tc.want)
		}
		caps, _ := json.Marshal(r["capabilities"])
		if string(caps) != `{"tools":{"listChanged":false}}` {
			t.Errorf("capabilities = %s", caps)
		}
		info := r["serverInfo"].(map[string]any)
		if info["name"] != "marvel-handoff" || info["version"] != "vtest" || len(info) != 2 {
			t.Errorf("serverInfo = %v", info)
		}
		if len(r) != 3 {
			t.Errorf("result carries extra fields: %v", r)
		}
	}
}

// TestMCPHandoffListsExactlyOneTool.
func TestMCPHandoffListsExactlyOneTool(t *testing.T) {
	in := mcpInit + `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"cursor":"x"}}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n"
	ls := runMCP(t, nil, time.Second, in).lines(t)
	if len(ls) != 3 {
		t.Fatalf("%d replies", len(ls))
	}
	r := resultOf(t, nthReply(t, ls, 1))
	if _, ok := r["nextCursor"]; ok {
		t.Errorf("nextCursor present: %v", r)
	}
	tools, _ := r["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want exactly one", r["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "submit_handoff" || tool["title"] != "Submit this seat's handoff" {
		t.Errorf("tool = %v", tool)
	}
	for _, k := range []string{"outputSchema", "annotations"} {
		if _, ok := tool[k]; ok {
			t.Errorf("tool carries %s", k)
		}
	}
	schema, _ := json.Marshal(tool["inputSchema"])
	var s struct {
		Type       string                    `json:"type"`
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
		Additional *bool                     `json:"additionalProperties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatal(err)
	}
	if s.Type != "object" || len(s.Properties) != 1 || s.Properties["text"]["type"] != "string" ||
		len(s.Required) != 1 || s.Required[0] != "text" || s.Additional == nil || *s.Additional {
		t.Errorf("inputSchema = %s", schema)
	}
	if got := resultOf(t, nthReply(t, ls, 2)); len(got) != 0 {
		t.Errorf("ping result = %v, want {}", got)
	}
}

// TestMCPHandoffSendsOnePutWithTokenAndText covers both token deliveries.
func TestMCPHandoffSendsOnePutWithTokenAndText(t *testing.T) {
	dir := t.TempDir()
	tokFile := filepath.Join(dir, "tok")
	if err := os.WriteFile(tokFile, []byte(mcpTestToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string]func(sock string) map[string]string{
		"variable": func(s string) map[string]string {
			return map[string]string{config.SocketEnv: s, api.HeartbeatTokenEnv: mcpTestToken}
		},
		"file": func(s string) map[string]string {
			return map[string]string{config.SocketEnv: s, api.HeartbeatTokenFileEnv: tokFile}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newMCPFake(t)
			text := mcpTestText + "\nline two\twith tab\r\n"
			run := runMCP(t, env(f.socket), 5*time.Second, mcpInit+mcpCall(2, mcpTextArg(t, text)))
			ls := run.lines(t)
			r := resultOf(t, nthReply(t, ls, 1))
			if r["isError"] != false {
				t.Fatalf("isError = %v: %v", r["isError"], r)
			}
			content := r["content"].([]any)
			if len(content) != 1 || content[0].(map[string]any)["text"] != "handoff written: /h/seat.md (17 bytes)" {
				t.Errorf("content = %v", content)
			}
			reqs := f.requests()
			if len(reqs) != 1 || reqs[0].Method != "handoff.put" {
				t.Fatalf("requests = %v", reqs)
			}
			var raw map[string]any
			if err := json.Unmarshal(reqs[0].Params, &raw); err != nil {
				t.Fatal(err)
			}
			if len(raw) != 2 || raw["token"] != mcpTestToken || raw["text"] != text {
				t.Errorf("params = %s, want exactly token and text", reqs[0].Params)
			}
		})
	}
}

// TestMCPHandoffPutFailuresAreErrorsNeverSuccess.
func TestMCPHandoffPutFailuresAreErrorsNeverSuccess(t *testing.T) {
	cases := map[string]struct {
		reply   func(daemonrpc.Request) *daemonrpc.Response
		timeout time.Duration
		dead    bool
		want    string
	}{
		"not requested": {reply: func(daemonrpc.Request) *daemonrpc.Response {
			return &daemonrpc.Response{Error: "no handoff is requested for this seat"}
		}, want: "no handoff is requested"},
		"unauthorized": {reply: func(daemonrpc.Request) *daemonrpc.Response {
			return &daemonrpc.Response{Error: "unauthorized\x1b[31m"}
		}, want: "unauthorized"},
		"too large": {reply: func(daemonrpc.Request) *daemonrpc.Response {
			return &daemonrpc.Response{Error: "handoff text is too large"}
		}, want: "too large"},
		"undecodable": {reply: func(daemonrpc.Request) *daemonrpc.Response {
			return &daemonrpc.Response{Result: json.RawMessage(`"not an object"`)}
		}, want: "decode"},
		"empty result": {reply: func(daemonrpc.Request) *daemonrpc.Response { return &daemonrpc.Response{} }, want: "decode"},
		"timeout":      {reply: func(daemonrpc.Request) *daemonrpc.Response { return nil }, timeout: 300 * time.Millisecond, want: "may or may not"},
		"unreachable":  {dead: true, want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newMCPFake(t)
			sock := f.socket
			if tc.dead {
				sock = filepath.Join(filepath.Dir(f.socket), "gone.sock")
			} else {
				f.reply = tc.reply
			}
			to := tc.timeout
			if to == 0 {
				to = 5 * time.Second
			}
			run := runMCP(t, map[string]string{config.SocketEnv: sock, api.HeartbeatTokenEnv: mcpTestToken}, to, mcpInit+mcpCall(2, mcpTextArg(t, mcpTestText)))
			r := resultOf(t, nthReply(t, run.lines(t), 1))
			if r["isError"] != true {
				t.Fatalf("isError = %v, want true: %v", r["isError"], r)
			}
			txt := r["content"].([]any)[0].(map[string]any)["text"].(string)
			if strings.Contains(txt, "handoff written") {
				t.Errorf("failure reads as success: %q", txt)
			}
			if !strings.Contains(txt, tc.want) {
				t.Errorf("text = %q, want it to contain %q", txt, tc.want)
			}
			if strings.ContainsAny(txt, "\x1b\x00") {
				t.Errorf("control character in %q", txt)
			}
			if strings.Contains(txt, mcpTestToken) || strings.Contains(run.stderr, mcpTestToken) {
				t.Errorf("token leaked")
			}
		})
	}
}

// TestMCPHandoffRefusesBeforeAnyConnection: each refused input leaves the
// fake daemon with zero connections.
func TestMCPHandoffRefusesBeforeAnyConnection(t *testing.T) {
	f := newMCPFake(t)
	env := map[string]string{config.SocketEnv: f.socket, api.HeartbeatTokenEnv: mcpTestToken}
	big := strings.Repeat("a", api.MaxHandoffPutBytes+1)
	tooLong := `{"jsonrpc":"2.0","id":9,"method":"ping","params":{"x":"` + strings.Repeat("b", 512*1024) + `"}}` + "\n"
	type tc struct {
		name, input string
		isError     bool    // tool result isError:true
		code        float64 // JSON-RPC error code, when not isError
	}
	cases := []tc{
		{"over size", mcpInit + mcpCall(2, mcpTextArg(t, big)), true, 0},
		{"empty", mcpInit + mcpCall(2, mcpTextArg(t, " \n\t")), true, 0},
		{"control char", mcpInit + mcpCall(2, mcpTextArg(t, "a\x07b")), true, 0},
		{"NUL", mcpInit + mcpCall(2, mcpTextArg(t, "a\x00b")), true, 0},
		{"extra path", mcpInit + mcpCall(2, `{"text":"x","path":"/etc/x"}`), false, -32602},
		{"extra session", mcpInit + mcpCall(2, `{"text":"x","session":"s"}`), false, -32602},
		{"extra token", mcpInit + mcpCall(2, `{"text":"x","token":"t"}`), false, -32602},
		{"extra socket", mcpInit + mcpCall(2, `{"text":"x","socket":"/s"}`), false, -32602},
		{"missing text", mcpInit + mcpCall(2, `{}`), false, -32602},
		{"non-string text", mcpInit + mcpCall(2, `{"text":5}`), false, -32602},
		{"null text", mcpInit + mcpCall(2, `{"text":null}`), false, -32602},
		{"unknown tool", mcpInit + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"other","arguments":{"text":"x"}}}` + "\n", false, -32602},
		{"call before initialize", mcpCall(2, mcpTextArg(t, "x")), false, -32600},
		{"second initialize", mcpInit + mcpInit, false, -32600},
		{"unknown method", mcpInit + `{"jsonrpc":"2.0","id":2,"method":"resources/list"}` + "\n", false, -32601},
	}
	for _, c := range cases {
		run := runMCP(t, env, 2*time.Second, c.input)
		ls := run.lines(t)
		last := nthReply(t, ls, -1)
		if c.isError {
			if resultOf(t, last)["isError"] != true {
				t.Errorf("%s: want isError true: %v", c.name, last)
			}
		} else if got := errCode(t, last); got != c.code {
			t.Errorf("%s: code = %v, want %v", c.name, got, c.code)
		}
	}
	// A batch is refused under every version.
	for _, v := range []string{"2025-06-18", "2025-03-26", "2024-11-05"} {
		in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + v + `"}}` + "\n" +
			`[{"jsonrpc":"2.0","id":2,"method":"ping"}]` + "\n"
		ls := runMCP(t, env, time.Second, in).lines(t)
		if errCode(t, nthReply(t, ls, 1)) != -32600 || nthReply(t, ls, 1)["id"] != nil {
			t.Errorf("batch under %s: %v", v, nthReply(t, ls, 1))
		}
	}
	// Invalid UTF-8 on the raw line is refused before decoding; the same text
	// with the control shown in valid UTF-8 passes the check (and sends).
	bad := mcpInit + "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"submit_handoff\",\"arguments\":{\"text\":\"a\xffb\"}}}\n"
	ls := runMCP(t, env, time.Second, bad).lines(t)
	if errCode(t, nthReply(t, ls, -1)) != -32700 {
		t.Errorf("invalid UTF-8: %v", nthReply(t, ls, -1))
	}
	// Unparseable line.
	ls = runMCP(t, env, time.Second, mcpInit+"{not json\n").lines(t)
	if errCode(t, nthReply(t, ls, -1)) != -32700 || nthReply(t, ls, -1)["id"] != nil {
		t.Errorf("unparseable: %v", nthReply(t, ls, -1))
	}
	// Over-long line: one -32600 with id null, then a nonzero exit.
	run := runMCP(t, env, time.Second, mcpInit+tooLong+mcpCall(3, mcpTextArg(t, "x")))
	ls = run.lines(t)
	if len(ls) != 2 || errCode(t, nthReply(t, ls, 1)) != -32600 || nthReply(t, ls, 1)["id"] != nil || run.err == nil {
		t.Errorf("over-long line: replies=%d err=%v out=%.200s", len(ls), run.err, run.stdout)
	}
	if n := f.conns.Load(); n != 0 {
		t.Fatalf("fake daemon saw %d connections, want 0", n)
	}
	// Control: the valid-UTF-8 form of the same control text does connect.
	ok := mcpInit + mcpCall(2, `{"text":"aÿb"}`)
	if resultOf(t, nthReply(t, runMCP(t, env, 2*time.Second, ok).lines(t), 1))["isError"] != false {
		t.Errorf("valid UTF-8 text was refused")
	}
	if n := f.conns.Load(); n != 1 {
		t.Errorf("control: %d connections, want 1", n)
	}
}

// TestMCPHandoffKeepsStdoutAndStderrClean: stdout is only JSON-RPC lines even
// when the text carries newlines, and neither stream carries the token or the
// text.
func TestMCPHandoffKeepsStdoutAndStderrClean(t *testing.T) {
	f := newMCPFake(t)
	f.reply = func(daemonrpc.Request) *daemonrpc.Response {
		return &daemonrpc.Response{Error: "refused " + mcpTestText + " " + mcpTestToken}
	}
	env := map[string]string{config.SocketEnv: f.socket, api.HeartbeatTokenEnv: mcpTestToken}
	text := "one\ntwo " + mcpTestText + "\nthree\n"
	run := runMCP(t, env, 2*time.Second, mcpInit+mcpCall(2, mcpTextArg(t, text))+"{not json "+mcpTestText+"\n")
	ls := run.lines(t) // fails on any non-JSON-RPC stdout line
	if len(ls) != 3 {
		t.Fatalf("%d replies: %s", len(ls), run.stdout)
	}
	if !strings.HasSuffix(run.stdout, "}\n") || strings.Contains(run.stdout, "\n\n") {
		t.Errorf("framing: %q", run.stdout)
	}
	if strings.Contains(run.stderr, mcpTestToken) || strings.Contains(run.stderr, mcpTestText) {
		t.Errorf("stderr carries the token or the text: %q", run.stderr)
	}
	if strings.Contains(run.stdout, mcpTestToken) {
		t.Errorf("stdout carries the token: %q", run.stdout)
	}
}

// TestMCPHandoffDialsOnlyItsOwnSocketEnv: with no MARVEL_SOCKET the tool
// fails and the layout default socket is never dialed; the root flags make it
// refuse to start.
func TestMCPHandoffDialsOnlyItsOwnSocketEnv(t *testing.T) {
	home := os.Getenv("HOME")
	def := config.DefaultSocket()
	if err := os.MkdirAll(filepath.Dir(def), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", def)
	if err != nil {
		t.Skipf("cannot listen at the layout default under %s: %v", home, err)
	}
	var hit atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			hit.Add(1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })

	for name, env := range map[string]map[string]string{
		"no socket":    {api.HeartbeatTokenEnv: mcpTestToken},
		"mrvl address": {config.SocketEnv: "mrvl://host", api.HeartbeatTokenEnv: mcpTestToken},
		"ssh address":  {config.SocketEnv: "ssh://host/x", api.HeartbeatTokenEnv: mcpTestToken},
	} {
		run := runMCP(t, env, time.Second, mcpInit+mcpCall(2, mcpTextArg(t, "x")))
		if resultOf(t, nthReply(t, run.lines(t), 1))["isError"] != true {
			t.Errorf("%s: want isError true", name)
		}
	}
	// No token is also a tool error.
	if resultOf(t, nthReply(t, runMCP(t, map[string]string{config.SocketEnv: def}, time.Second, mcpInit+mcpCall(2, mcpTextArg(t, "x"))).lines(t), 1))["isError"] != true {
		t.Errorf("no token: want isError true")
	}
	if n := hit.Load(); n != 0 {
		t.Fatalf("layout default socket dialed %d times", n)
	}

	// The root flags are package state a later test would inherit.
	t.Cleanup(func() {
		socketPath, clusterName, identityPath, clusterFlagGiven = "", "", "", false
	})

	// Control: with no root flag the command serves.
	ctl := newRootCmd()
	var ctlOut bytes.Buffer
	ctl.SetIn(strings.NewReader(mcpInit))
	ctl.SetOut(&ctlOut)
	ctl.SetErr(&bytes.Buffer{})
	ctl.SetArgs([]string{"mcp", "handoff"})
	if err := ctl.Execute(); err != nil || !strings.Contains(ctlOut.String(), "protocolVersion") {
		t.Errorf("control: mcp handoff without flags did not serve: err=%v out=%q", err, ctlOut.String())
	}

	// Root flags: the command refuses to start and dials nothing.
	for _, args := range [][]string{{"--socket", def}, {"--cluster", "prod"}} {
		root := newRootCmd()
		var out, errb bytes.Buffer
		root.SetIn(strings.NewReader(mcpInit))
		root.SetOut(&out)
		root.SetErr(&errb)
		root.SetArgs(append(args, "mcp", "handoff"))
		if err := root.Execute(); err == nil {
			t.Errorf("%v: command started, want a refusal", args)
		}
		if strings.Contains(out.String(), "protocolVersion") {
			t.Errorf("%v: served despite a root flag", args)
		}
	}
	if n := hit.Load(); n != 0 {
		t.Fatalf("layout default socket dialed %d times after flags", n)
	}
}
