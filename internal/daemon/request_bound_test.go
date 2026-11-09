package daemon

import (
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// boundCallers are the two paths into handleRWCAs: a tunnelled caller, never
// local, as the SSH server builds it, and the daemon's own unix socket.
var boundCallers = []struct {
	name string
	c    caller
}{
	{"ssh", caller{scope: ScopeAdmin, fingerprint: "SHA256:bound-test"}},
	{"local", localCaller()},
}

// paddedVersionRequest is a valid version request whose params carry a string
// of the given size, so the request is that many bytes plus a small envelope.
func paddedVersionRequest(t *testing.T, pad int) []byte {
	t.Helper()
	params, err := json.Marshal(map[string]string{"pad": strings.Repeat("x", pad)})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(daemonrpc.Request{Method: daemonrpc.MethodVersion, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// serveBounded runs handleRWCAs for c on one end of a pipe, writes body from the
// other, and returns the response with the number of bytes the daemon took.
func serveBounded(t *testing.T, d *Daemon, c caller, body []byte) (daemonrpc.Response, int64) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleRWCAs(server, c)
	}()
	var taken atomic.Int64
	go func() {
		const chunk = 64 << 10
		for off := 0; off < len(body); off += chunk {
			end := min(off+chunk, len(body))
			n, err := client.Write(body[off:end])
			taken.Add(int64(n))
			if err != nil {
				return
			}
		}
	}()
	var resp daemonrpc.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("read response: %v", err)
	}
	_, _ = io.Copy(io.Discard, client)
	_ = client.Close()
	<-done
	return resp, taken.Load()
}

// marvel#1197: the daemon decodes each request before it knows the method, so
// the one place a size limit can apply to every method is the reader under
// that decode. A request past 24 MiB is refused with a size error and the
// handler never runs.
func TestRequestOverTheBoundIsRefusedBeforeDispatch(t *testing.T) {
	for _, p := range boundCallers {
		t.Run(p.name, func(t *testing.T) { requestOverTheBoundIsRefused(t, p.c) })
	}
}

func requestOverTheBoundIsRefused(t *testing.T, c caller) {
	t.Helper()
	d := newHandlerDaemon(t)

	over := paddedVersionRequest(t, (24<<20)+1)
	resp, _ := serveBounded(t, d, c, over)
	if !strings.Contains(resp.Error, "request too large") || !strings.Contains(resp.Error, "24 MiB") {
		t.Fatalf("error = %q result=%.80s, want a size error naming 24 MiB", resp.Error, resp.Result)
	}
	if len(resp.Result) != 0 {
		t.Errorf("result = %s, want none: the handler must not run on a refused request", resp.Result)
	}

	// Control: a request of exactly 24 MiB is served, one byte less than refused.
	base := len(paddedVersionRequest(t, 0))
	exact := paddedVersionRequest(t, (24<<20)-base)
	if len(exact) != 24<<20 {
		t.Fatalf("exact request is %d bytes, want %d", len(exact), 24<<20)
	}
	if at, _ := serveBounded(t, d, c, exact); at.Error != "" || len(at.Result) == 0 {
		t.Fatalf("a request of exactly 24 MiB: error %q result %.60s, want it dispatched", at.Error, at.Result)
	}

	// Control: a request of the same shape, well under the bound, is served.
	ok, _ := serveBounded(t, d, c, paddedVersionRequest(t, 1<<20))
	if ok.Error != "" || len(ok.Result) == 0 {
		t.Fatalf("1 MiB request: error %q result %s, want it dispatched", ok.Error, ok.Result)
	}
}

// The refusal comes while the body is still arriving: a client that offers far
// more than the bound is cut off one byte past it, so the daemon does not buffer what
// it will refuse.
func TestRequestOverTheBoundIsCutOffNearTheBound(t *testing.T) {
	for _, p := range boundCallers {
		t.Run(p.name, func(t *testing.T) { requestOverTheBoundIsCutOff(t, p.c) })
	}
}

func requestOverTheBoundIsCutOff(t *testing.T, c caller) {
	t.Helper()
	d := newHandlerDaemon(t)
	offered := paddedVersionRequest(t, 64<<20)
	resp, taken := serveBounded(t, d, c, offered)
	if !strings.Contains(resp.Error, "request too large") {
		t.Fatalf("error = %q, want a size error", resp.Error)
	}
	if limit := int64(24<<20) + 1; taken > limit {
		t.Errorf("the daemon took %d bytes of %d offered, want it cut off one byte past 24 MiB (%d)", taken, len(offered), limit)
	}

	// Control: a small request is read to its end.
	small := paddedVersionRequest(t, 1000)
	if _, taken = serveBounded(t, d, c, small); taken != int64(len(small)) {
		t.Errorf("small request: took %d of %d bytes, want all of it", taken, len(small))
	}
}
