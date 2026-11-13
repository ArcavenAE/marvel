package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/daemon"
)

// shortTempDir is a scratch directory with a short path, so a unix socket in
// it stays under the 104-character limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// silentDaemon listens on a unix socket in a short scratch directory, accepts
// every connection and never answers, the way a daemon that has stopped
// serving does. It returns the socket path.
func silentDaemon(t *testing.T) string {
	t.Helper()
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var conns []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		<-done
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return sock
}

// watchRig runs the real watchLoop for a single-cluster view whose fetch
// answers at once, with the target resolver the program installs. The daemon
// the client would dial is the silent one at sock.
type watchRig struct {
	keys   chan byte
	frames chan string
	done   chan struct{}
}

func startWatch(t *testing.T, sock string, header headerInfo) *watchRig {
	t.Helper()
	t.Setenv("MARVEL_SOCKET", sock)
	t.Setenv("TMUX", "")
	setWidth(t, 200)
	cols, _, err := loadSessionColumnsSel("")
	if err != nil {
		t.Fatal(err)
	}
	ws := newWatchState(cols)
	ws.target = watchTargetFor(ws)
	ws.exec = func(argv []string) error {
		t.Errorf("Enter ran %v; in this slice it only shows the plan", argv)
		return nil
	}
	rig := &watchRig{
		keys:   make(chan byte, 8),
		frames: make(chan string, 64),
		done:   make(chan struct{}),
	}
	cfg := watchLoopConfig{
		interval: time.Hour,
		deadline: time.Hour,
		fetch: func() watchData {
			return watchData{sessions: selRows(2), header: header, at: watchT0}
		},
		now: func() time.Time { return watchT0 },
		out: func(f string) {
			select {
			case rig.frames <- f:
			default:
			}
		},
	}
	go func() { watchLoop(ws, cfg, rig.keys, make(chan time.Time)); close(rig.done) }()
	t.Cleanup(func() {
		select {
		case rig.keys <- 'q':
		default:
		}
	})
	return rig
}

func (r *watchRig) frameWith(t *testing.T, want string) string {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case f := <-r.frames:
			if strings.Contains(f, want) {
				return f
			}
		case <-deadline:
			t.Fatalf("no frame with %q within 1s", want)
			return ""
		}
	}
}

func daemonHeader(sock string) headerInfo {
	return headerInfo{Address: sock, Status: &daemon.DaemonStatus{TmuxSocket: "sockT"}}
}

// Enter plans from the status the watch already fetched, so no call is made
// to the daemon on the key. A daemon that accepts and never answers must not
// hold the view: q quits at once after an Enter.
func TestEnterNeverWaitsOnTheDaemon(t *testing.T) {
	sock := silentDaemon(t)
	r := startWatch(t, sock, daemonHeader(sock))
	r.frameWith(t, "seat00") // the first fetch has landed

	r.keys <- 'j'
	r.keys <- 'j'
	r.keys <- 13
	r.keys <- 'q'
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("q did not quit within 2s while the daemon was not answering after an Enter")
	}
}

func TestEnterPlansFromTheStatusTheWatchFetched(t *testing.T) {
	sock := silentDaemon(t)
	r := startWatch(t, sock, daemonHeader(sock))
	r.frameWith(t, "seat00")
	r.keys <- 'j'
	r.keys <- 'j'
	r.keys <- 13
	r.frameWith(t, "plan: tmux -L sockT attach -t %101")
}

func TestEnterSaysWhenTheWatchHasNoDaemonStatus(t *testing.T) {
	sock := silentDaemon(t)
	r := startWatch(t, sock, headerInfo{Address: sock, StatusErr: "no answer within 5s"})
	r.frameWith(t, "seat00")
	r.keys <- 'j'
	r.keys <- 13
	r.frameWith(t, "cannot plan aae/seat00: no daemon status yet (no answer within 5s)")
}

func TestCutTableSaysWhichRowsAreShown(t *testing.T) {
	ws := selRig(t, selRows(30))
	ws.height = 10
	for i := 0; i < 26; i++ {
		press(t, ws, 'j')
	}
	frame := frameOf(ws)
	first, last := -1, -1
	for i := 0; i < 30; i++ {
		if strings.Contains(frame, fmt.Sprintf("seat%02d", i)) {
			if first < 0 {
				first = i + 1
			}
			last = i + 1
		}
	}
	want := fmt.Sprintf("showing rows %d-%d of 30", first, last)
	if !strings.Contains(frame, want) {
		t.Errorf("a cut table should say %q:\n%s", want, frame)
	}
	if n := len(strings.Split(strings.TrimRight(frame, "\n"), "\n")); n > ws.height {
		t.Errorf("with the line added the frame is %d lines, want at most %d:\n%s", n, ws.height, frame)
	}

	whole := selRig(t, selRows(3))
	whole.height = 10
	if f := frameOf(whole); strings.Contains(f, "showing rows") {
		t.Errorf("a table that fits should not say which rows are shown:\n%s", f)
	}
}
