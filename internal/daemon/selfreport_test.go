package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

// pipeRWC feeds handleRWC a fixed request and captures what it writes.
type pipeRWC struct {
	in  *bytes.Reader
	out bytes.Buffer
}

func newPipeRWC(request string) *pipeRWC {
	return &pipeRWC{in: bytes.NewReader([]byte(request))}
}

func (p *pipeRWC) Read(b []byte) (int, error)  { return p.in.Read(b) }
func (p *pipeRWC) Write(b []byte) (int, error) { return p.out.Write(b) }
func (p *pipeRWC) Close() error                { return nil }

var _ io.ReadWriteCloser = (*pipeRWC)(nil)

// serve runs one request through the daemon's write path and decodes the
// reply the way a client would. A bare &Daemon is enough: the methods
// exercised here answer before touching the store or the tmux driver, so
// the test does not need a real daemon or a tmux binary.
func serve(t *testing.T, home, request string) Response {
	t.Helper()
	rwc := newPipeRWC(request)
	d := &Daemon{home: home}
	d.handleRWC(rwc)

	var resp Response
	if err := json.Unmarshal(rwc.out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", rwc.out.String(), err)
	}
	return resp
}

// The field has to reach the client on the wire, not merely exist on the
// struct: it is stamped on the write path, so any method gets it.
func TestResponseCarriesDaemonHome(t *testing.T) {
	t.Parallel()

	const home = "/Users/op/.marvel"

	tests := []struct {
		name    string
		request string
	}{
		{
			name:    "dispatched method",
			request: `{"method":"nosuchmethod"}`,
		},
		{
			// The decode-error reply is written before dispatch, and it
			// is worth attributing too: a malformed request answered by
			// the wrong daemon is the same diagnostic problem.
			name:    "undecodable request",
			request: `{not json`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := serve(t, home, tt.request)
			if resp.Error == "" {
				t.Fatal("expected an error reply from this fixture")
			}
			if resp.DaemonHome != home {
				t.Errorf("daemon_home: want %q, got %q", home, resp.DaemonHome)
			}
		})
	}
}

// A daemon that cannot resolve its home omits the field rather than
// reporting an empty string, which is what keeps an old client's parse
// identical and gives a new client something unambiguous to skip.
func TestUnresolvedHomeOmitsField(t *testing.T) {
	t.Parallel()

	rwc := newPipeRWC(`{"method":"nosuchmethod"}`)
	d := &Daemon{}
	d.handleRWC(rwc)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rwc.out.Bytes(), &raw); err != nil {
		t.Fatalf("decode response %q: %v", rwc.out.String(), err)
	}
	if _, ok := raw["daemon_home"]; ok {
		t.Errorf("expected daemon_home to be omitted, got %s", rwc.out.String())
	}
}

// Wire compatibility, both directions. No version field and no handshake
// ship with this change, so the envelope has to stay readable by clients
// and daemons that predate the field.
func TestEnvelopeWireCompatibility(t *testing.T) {
	t.Parallel()

	t.Run("old client ignores the new field", func(t *testing.T) {
		t.Parallel()
		// The envelope as it was before this change.
		type oldResponse struct {
			Result json.RawMessage `json:"result,omitempty"`
			Error  string          `json:"error,omitempty"`
		}
		wire, err := json.Marshal(Response{
			Result:     json.RawMessage(`{"status":"ok"}`),
			DaemonHome: "/Users/op/.marvel",
		})
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		var old oldResponse
		if err := json.Unmarshal(wire, &old); err != nil {
			t.Fatalf("old client failed to parse %s: %v", wire, err)
		}
		if string(old.Result) != `{"status":"ok"}` {
			t.Errorf("result: got %s", old.Result)
		}
	})

	t.Run("new client tolerates a daemon without the field", func(t *testing.T) {
		t.Parallel()
		var resp Response
		if err := json.Unmarshal([]byte(`{"result":{"status":"ok"}}`), &resp); err != nil {
			t.Fatalf("parse old daemon reply: %v", err)
		}
		if resp.DaemonHome != "" {
			t.Errorf("daemon_home: want empty, got %q", resp.DaemonHome)
		}
	})
}
