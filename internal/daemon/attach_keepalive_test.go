package daemon

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// silentProxy sits between the client and the daemon's real SSH listener and
// forwards bytes until told to go silent, after which it reads and drops them
// in both directions: the link of a laptop whose lid just closed. The daemon
// behind it is the one that serves attach, through its own accept path.
type silentProxy struct {
	ln     net.Listener
	silent atomic.Bool
	mu     sync.Mutex
	conns  []net.Conn
}

func newSilentProxy(t *testing.T, target string) *silentProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &silentProxy{ln: ln}
	t.Cleanup(func() {
		_ = ln.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, c := range p.conns {
			_ = c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, up)
			p.mu.Unlock()
			go p.pipe(up, c)
			go p.pipe(c, up)
		}
	}()
	return p
}

func (p *silentProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.silent.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *silentProxy) addr() string { return "mrvl://op@" + p.ln.Addr().String() }

// attachVia runs daemonrpc.Attach to the proxy with the given keepalive.
func attachVia(t *testing.T, r *attachRig, addr string, every time.Duration, misses int) (in *io.PipeWriter, out *attachOut, done <-chan error) {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	out = &attachOut{}
	res := make(chan error, 1)
	go func() {
		res <- daemonrpc.Attach(addr, "23",
			daemonrpc.DialOptions{Identity: r.identity, TrustUnknownHost: true},
			daemonrpc.AttachIO{In: pr, Out: out, Rows: 24, Cols: 80, KeepAlive: every, KeepAliveMisses: misses})
	}()
	return pw, out, res
}

// The card's lid check at the client: when the daemon stops answering, Attach
// ends with a lost-contact error in about interval times misses, and does not
// wait on the dead connection.
func TestAttachReturnsLostContactWhenTheLinkGoesSilent(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("echo ready; exec cat"))
	p := newSilentProxy(t, r.srv.Addr().String())
	_, out, done := attachVia(t, r, p.addr(), 50*time.Millisecond, 3)
	out.waitFor(t, "ready")
	p.silent.Store(true)
	select {
	case err := <-done:
		if !errors.Is(err, daemonrpc.ErrLostContact) {
			t.Fatalf("Attach error = %v, want ErrLostContact in its chain", err)
		}
		var lc *daemonrpc.LostContactError
		if !errors.As(err, &lc) || lc.Silent != 150*time.Millisecond {
			t.Errorf("silent = %v, want 150ms (interval times misses)", lc)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Attach was still blocked 2s after the link went silent")
	}
}

// A healthy link with keepalives running is not mistaken for a dead one. The
// daemon answers each keepalive through its own accept path (it drains global
// requests and replies false), and a false reply counts as alive.
func TestAttachKeepsRunningOnAHealthyLinkWithKeepalives(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("echo ready; exec cat"))
	p := newSilentProxy(t, r.srv.Addr().String())
	in, out, done := attachVia(t, r, p.addr(), 50*time.Millisecond, 3)
	out.waitFor(t, "ready")
	select {
	case err := <-done:
		t.Fatalf("Attach ended after 1s of healthy keepalives: %v", err)
	case <-time.After(time.Second):
	}
	if _, err := in.Write([]byte{4}); err != nil {
		t.Fatalf("write ctrl-d: %v", err)
	}
	waitAttachDone(t, done)
}
