package daemonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func lowerClientRequestBound(t *testing.T, n int64) {
	t.Helper()
	old := maxRequestBytes
	maxRequestBytes = n
	t.Cleanup(func() { maxRequestBytes = old })
}

// encodedSize is what SendRequestWith would put on the wire for req: the
// encoder's output, with its HTML escapes and trailing newline.
func encodedSize(t *testing.T, req Request) int {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(req); err != nil {
		t.Fatal(err)
	}
	return buf.Len()
}

// requestEncodingTo is a version request whose encoding is exactly n bytes.
func requestEncodingTo(t *testing.T, n int) Request {
	t.Helper()
	build := func(pad int) Request {
		params, err := json.Marshal(map[string]string{"pad": strings.Repeat("x", pad)})
		if err != nil {
			t.Fatal(err)
		}
		return Request{Method: MethodVersion, Params: params}
	}
	req := build(n - encodedSize(t, build(0)))
	if got := encodedSize(t, req); got != n {
		t.Fatalf("request encodes to %d bytes, want %d", got, n)
	}
	return req
}

// Orc outcome test 7 (marvel#1197): the client measures the encoded request
// and refuses one byte over the bound before it dials, so every method's
// caller sees the size error. The socket path does not exist, so a dial would
// show a dial error and not this one. The control, one byte under, reaches
// the dial.
func TestClientRefusesAnOversizedRequestBeforeDialing(t *testing.T) {
	lowerClientRequestBound(t, 1<<10)
	sock := filepath.Join(t.TempDir(), "absent.sock")

	_, err := SendRequestWith(sock, requestEncodingTo(t, 1<<10+1), DialOptions{})
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("one byte over: err = %v, want ErrRequestTooLarge", err)
	}
	if !strings.Contains(err.Error(), "1 KiB") {
		t.Errorf("error %q does not name the bound, 1 KiB", err)
	}

	_, err = SendRequestWith(sock, requestEncodingTo(t, 1<<10), DialOptions{})
	if err == nil || errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("exactly at the bound: err = %v, want a dial error", err)
	}
}

// The encoded size is what counts: a 1 KiB string of "<" is 1 KiB of params
// and encodes to 6 KiB, because the encoder escapes each one.
func TestClientMeasuresTheEncodedSize(t *testing.T) {
	lowerClientRequestBound(t, 2<<10)
	params, err := json.Marshal(map[string]string{"s": strings.Repeat("<", 1<<10)})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Method: MethodVersion, Params: params}
	if n := encodedSize(t, req); n <= 2<<10 {
		t.Fatalf("encoded size %d, want it past 2 KiB for the test to mean anything", n)
	}
	_, err = SendRequestWith(filepath.Join(t.TempDir(), "absent.sock"), req, DialOptions{})
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("err = %v, want ErrRequestTooLarge for a request that encodes past the bound", err)
	}
}

// Test 8, client half: the defaults are pinned, request 24 MiB and reply 32
// MiB, so a lowered test variable cannot hide a changed default.
func TestRequestAndReplyBoundDefaults(t *testing.T) {
	if maxRequestBytes != 24<<20 || DefaultMaxRequestBytes != 24<<20 {
		t.Errorf("request bound %d, default %d, want 24 MiB", maxRequestBytes, DefaultMaxRequestBytes)
	}
	if maxReplyBytes != 32<<20 || defaultMaxReplyBytes != 32<<20 {
		t.Errorf("reply bound %d, default %d, want 32 MiB", maxReplyBytes, defaultMaxReplyBytes)
	}
}

func TestBoundText(t *testing.T) {
	t.Parallel()
	for n, want := range map[int64]string{24 << 20: "24 MiB", 1 << 10: "1 KiB", 1500: "1500 bytes", 0: "0 bytes", 3 << 20: "3 MiB"} {
		if got := BoundText(n); got != want {
			t.Errorf("BoundText(%d) = %q, want %q", n, got, want)
		}
	}
}
