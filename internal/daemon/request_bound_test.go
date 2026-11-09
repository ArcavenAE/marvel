package daemon

import (
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	return serveChunked(t, d, c, body, 64<<10)
}

// serveChunked is serveBounded with the body written chunk bytes at a time.
func serveChunked(t *testing.T, d *Daemon, c caller, body []byte, chunk int) (daemonrpc.Response, int64) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleRWCAs(server, c)
	}()
	var taken atomic.Int64
	go func() {
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

// lowerRequestBound sets the daemon's bound to 1 KiB for one test and puts it back. The
// tests that use it do not run in parallel.
func lowerRequestBound(t *testing.T) {
	t.Helper()
	old := maxRequestBytes
	maxRequestBytes = 1 << 10
	t.Cleanup(func() { maxRequestBytes = old })
}

// requestOfSize is a valid version request that is exactly n bytes.
func requestOfSize(t *testing.T, n int) []byte {
	t.Helper()
	body := paddedVersionRequest(t, n-len(paddedVersionRequest(t, 0)))
	if len(body) != n {
		t.Fatalf("request is %d bytes, want %d", len(body), n)
	}
	return body
}

// Orc outcome tests 1 and 2, with the bound lowered to 1 KiB: a 2 KiB request
// is refused with the size error naming the bound in force, on both paths.
func TestLoweredRequestBoundRefusesOnBothPaths(t *testing.T) {
	lowerRequestBound(t)
	d := newHandlerDaemon(t)
	for _, p := range boundCallers {
		t.Run(p.name, func(t *testing.T) {
			resp, _ := serveBounded(t, d, p.c, requestOfSize(t, 2<<10))
			if resp.Error != "request too large: over 1 KiB" || len(resp.Result) != 0 {
				t.Fatalf("2 KiB: error %q result %.40s, want the size error naming 1 KiB and no result", resp.Error, resp.Result)
			}
			ok, _ := serveBounded(t, d, p.c, requestOfSize(t, 1023))
			if ok.Error != "" || len(ok.Result) == 0 {
				t.Fatalf("1023 bytes: error %q, want it dispatched", ok.Error)
			}
		})
	}
}

// Test 3: the same request arriving in 100-byte writes is refused, and 900
// bytes in the same writes is decoded.
func TestLoweredRequestBoundHoldsForChunkedWrites(t *testing.T) {
	lowerRequestBound(t)
	d := newHandlerDaemon(t)
	resp, _ := serveChunked(t, d, localCaller(), requestOfSize(t, 2<<10), 100)
	if !strings.Contains(resp.Error, "request too large") {
		t.Fatalf("2 KiB in 100-byte writes: error %q, want the size error", resp.Error)
	}
	ok, _ := serveChunked(t, d, localCaller(), requestOfSize(t, 900), 100)
	if ok.Error != "" || len(ok.Result) == 0 {
		t.Fatalf("900 bytes in 100-byte writes: error %q, want it dispatched", ok.Error)
	}
}

// Test 4: a small malformed request keeps the decode error and never reads as
// a size error.
func TestMalformedSmallRequestKeepsTheDecodeError(t *testing.T) {
	d := newHandlerDaemon(t)
	bad := []byte(`{"method":` + strings.Repeat(" ", 30) + `]`)
	resp, _ := serveBounded(t, d, localCaller(), bad)
	if !strings.HasPrefix(resp.Error, "decode request:") || strings.Contains(resp.Error, "too large") {
		t.Fatalf("malformed request: error %q, want the decode request error", resp.Error)
	}
	ok, _ := serveBounded(t, d, localCaller(), []byte(`{"method":"version","params":{}}`))
	if ok.Error != "" {
		t.Fatalf("well-formed request: error %q, want it dispatched", ok.Error)
	}
}

// Test 5: events.watch is decoded in the same place, so over the bound it gets
// one size Response and no stream. Control: under the bound it answers.
func TestEventsWatchOverTheBoundGetsOneSizeResponse(t *testing.T) {
	lowerRequestBound(t)
	d := newHandlerDaemon(t)
	watch := func(pad int) (first daemonrpc.Response, more bool) {
		t.Helper()
		params, _ := json.Marshal(map[string]string{"pad": strings.Repeat("x", pad)})
		body, _ := json.Marshal(daemonrpc.Request{Method: daemonrpc.MethodEventsWatch, Params: params})
		client, server := net.Pipe()
		go d.handleRWCAs(server, localCaller())
		go func() { _, _ = client.Write(body) }()
		dec := json.NewDecoder(client)
		if err := dec.Decode(&first); err != nil {
			t.Fatalf("first response: %v", err)
		}
		var next daemonrpc.Response
		_ = client.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		more = dec.Decode(&next) == nil
		_ = client.Close()
		return first, more
	}
	first, _ := watch(2 << 10)
	if first.Error != "request too large: over 1 KiB" {
		t.Fatalf("over the bound: error %q, want the size error", first.Error)
	}
	if first, more := watch(2 << 10); more || first.Result != nil {
		t.Errorf("over the bound: more responses %v, result %.40s, want one size response and nothing after", more, first.Result)
	}
	if under, _ := watch(10); under.Error != "" {
		t.Errorf("under the bound: error %q, want the watch to answer", under.Error)
	}
}

// Test 6: the size reply is stamped like any other, so a client sees which
// daemon refused it.
func TestSizeReplyCarriesTheDaemonHome(t *testing.T) {
	lowerRequestBound(t)
	d := newHandlerDaemon(t)
	d.home = "/scratch/marvel-home"
	size, _ := serveBounded(t, d, localCaller(), requestOfSize(t, 2<<10))
	ok, _ := serveBounded(t, d, localCaller(), requestOfSize(t, 100))
	if size.DaemonHome != "/scratch/marvel-home" || size.DaemonHome != ok.DaemonHome {
		t.Fatalf("size reply home %q, version reply home %q, want both /scratch/marvel-home", size.DaemonHome, ok.DaemonHome)
	}
}

// Test 8, daemon half: the defaults are pinned, so a lowered test variable
// cannot hide a changed default.
func TestRequestBoundDefaultIs24MiB(t *testing.T) {
	if maxRequestBytes != 24<<20 || daemonrpc.DefaultMaxRequestBytes != 24<<20 {
		t.Fatalf("request bound = %d, default %d, want 24 MiB (%d)", maxRequestBytes, daemonrpc.DefaultMaxRequestBytes, 24<<20)
	}
}

// A request whose encoding is exactly one byte over the bound is refused, and
// one exactly at the bound is served. The decoder can complete a value on the
// very read that crosses the limit and drop that read's error, so the count is
// checked after the decode as well; this pins that check, which no other test
// reaches (marvel#1197).
func TestRequestExactlyOneByteOverTheBoundIsRefused(t *testing.T) {
	lowerRequestBound(t)
	d := newHandlerDaemon(t)
	for _, p := range boundCallers {
		t.Run(p.name, func(t *testing.T) {
			over, _ := serveBounded(t, d, p.c, requestOfSize(t, 1<<10+1))
			if over.Error != "request too large: over 1 KiB" || len(over.Result) != 0 {
				t.Fatalf("bound+1 bytes: error %q result %.40s, want the size error and no dispatch", over.Error, over.Result)
			}
			at, _ := serveBounded(t, d, p.c, requestOfSize(t, 1<<10))
			if at.Error != "" || len(at.Result) == 0 {
				t.Fatalf("exactly the bound: error %q, want it dispatched", at.Error)
			}
		})
	}
}
