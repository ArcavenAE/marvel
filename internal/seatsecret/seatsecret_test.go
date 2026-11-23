package seatsecret

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Every value here is a fake marker.

const paneVar = "MX_SEAT_SECRET"

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// runPane runs the wrapped command the way tmux does, through sh -c, with a
// body that writes the sha256 of the variable to out, and
// starts it.
func runPane(t *testing.T, h *Handover, wait time.Duration, out string) {
	t.Helper()
	body := `printf %s "$` + paneVar + `" | /usr/bin/shasum -a 256 | cut -d' ' -f1 > ` + out
	cmd := exec.Command("sh", "-c", h.Wrap(paneVar, body, wait))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
}

func readOut(t *testing.T, out string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			return strings.TrimSpace(string(b))
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ""
}

// The directory is private and holds one FIFO; nothing else.
func TestPrepareMakesAPrivateDirectoryWithOneFIFO(t *testing.T) {
	base := t.TempDir()
	h, err := Prepare(base)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("want one directory under base, got %v (%v)", entries, err)
	}
	info, _ := entries[0].Info()
	if info.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v, want 0700", info.Mode().Perm())
	}
	inner, _ := os.ReadDir(filepath.Join(base, entries[0].Name()))
	if len(inner) != 1 || inner[0].Type()&os.ModeNamedPipe == 0 {
		t.Errorf("want one FIFO in the directory, got %v", inner)
	}
}

// The pane gets the value, exactly, however odd: quotes, $(...), backticks and
// spaces are data, never code (no eval).
func TestThePaneReceivesTheValueExactly(t *testing.T) {
	for _, v := range []string{"mxfake-0042", `mx"fake'$(touch /tmp/mx-pwn)` + "`id`" + ` with spaces; and=equals`} {
		h, err := Prepare(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "out")
		runPane(t, h, 10*time.Second, out)
		if err := h.Deliver([]byte(v), 10*time.Second); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
		if got := readOut(t, out); got != sum(v) {
			t.Errorf("the pane received the wrong value (hash %s, want %s)", got, sum(v))
		}
		if _, err := os.Stat("/tmp/mx-pwn"); err == nil {
			t.Error("the value was run as code")
		}
	}
}

// Once delivered the directory and the FIFO are gone, and a second Close is safe.
func TestDeliverRemovesTheDirectory(t *testing.T) {
	base := t.TempDir()
	h, err := Prepare(base)
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(base); len(entries) != 1 {
		t.Fatalf("%d entries under base before delivery, want the one directory", len(entries))
	}
	out := filepath.Join(t.TempDir(), "out")
	runPane(t, h, 10*time.Second, out)
	if err := h.Deliver([]byte("mxfake-0042"), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	h.Close()
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Errorf("%d entries left under base after delivery", len(entries))
	}
}

// No pane ever reads: Deliver gives up within its bound, says so without the
// value, and leaves nothing behind.
func TestDeliverWithNoReaderFailsWithinItsBoundAndCleansUp(t *testing.T) {
	base := t.TempDir()
	h, err := Prepare(base)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = h.Deliver([]byte("mxfake-0042"), 300*time.Millisecond)
	if err == nil {
		t.Fatal("Deliver reported success with no reader")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Deliver took %v for a 300ms bound", d)
	}
	if strings.Contains(err.Error(), "mxfake-0042") {
		t.Error("the error carries the value")
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Errorf("%d entries left under base after a failed delivery", len(entries))
	}
}

// No writer ever comes: the pane's read gives up within its bound, the command
// still runs, and the variable is empty. A seat without the value, not a hang.
func TestAPaneWithNoWriterStartsWithoutTheValueWithinItsBound(t *testing.T) {
	h, err := Prepare(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	out := filepath.Join(t.TempDir(), "out")
	start := time.Now()
	runPane(t, h, 1*time.Second, out)
	if got := readOut(t, out); got != sum("") {
		t.Errorf("the command did not run with an empty variable (hash %q)", got)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Errorf("the pane waited %v for a 1s bound", d)
	}
}

// A value with a newline cannot ride a line-based hand-over and is refused
// before anything is written.
func TestAValueWithANewlineIsRefused(t *testing.T) {
	h, err := Prepare(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Deliver([]byte("mxfake\n0042"), 200*time.Millisecond); err == nil || strings.Contains(err.Error(), "mxfake") {
		t.Errorf("Deliver = %v; want a refusal that does not repeat the value", err)
	}
}

// The measurement behind the design, as a test: while the value is handed over,
// no process line carries it. The control puts it on a process's argument list
// and the sampler must see that, so a clean result means something.
func TestTheValueNeverAppearsOnAProcessArgumentList(t *testing.T) {
	// Built at run time, so no command line that launched the test carries it.
	marker := "mxfake-argv-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	pat := filepath.Join(t.TempDir(), "pat")
	if err := os.WriteFile(pat, []byte(marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var hits, samples atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			out, err := exec.Command("sh", "-c", `/bin/ps -axww -o command= | grep -c -F -f `+pat).Output()
			if err == nil && strings.TrimSpace(string(out)) != "0" {
				hits.Add(1)
			}
			samples.Add(1)
		}
	}()
	ctl := exec.Command("perl", "-e", "sleep 3", marker)
	if err := ctl.Start(); err != nil {
		t.Skipf("no perl for the control: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	control := hits.Load()
	_ = ctl.Process.Kill()
	_ = ctl.Wait()
	if control == 0 {
		close(stop)
		<-done
		t.Skip("the sampler cannot read the process table here, so the proof would pass for nothing")
	}
	// A sample already running when the control died can still count it. Wait for
	// two whole samples that started after the kill before the count restarts.
	for settled := samples.Load() + 2; samples.Load() < settled; {
		time.Sleep(20 * time.Millisecond)
	}
	hits.Store(0)
	h, err := Prepare(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	runPane(t, h, 10*time.Second, out)
	if err := h.Deliver([]byte(marker), 10*time.Second); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	readOut(t, out)
	time.Sleep(500 * time.Millisecond)
	close(stop)
	<-done
	if got := readOut(t, out); got != sum(marker) {
		t.Errorf("the pane did not receive the value (hash %s), so the clean sample proves nothing", got)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the value was on a process argument list in %d samples", n)
	}
}
