package daemon

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// fakePane serves fixed answers and records every call, so a test can say
// what was read and that nothing else was.
type fakePane struct {
	foreground string
	plain      string
	escapes    string
	err        error // fails every call
	captureErr error // fails only the captures
	calls      []string
}

func (f *fakePane) PaneForeground(id string) (string, int, error) {
	f.calls = append(f.calls, "foreground "+id)
	if f.err != nil {
		return "", 0, f.err
	}
	return f.foreground, 120, nil
}

func (f *fakePane) CapturePane(id string) (string, error) {
	f.calls = append(f.calls, "capture "+id)
	if f.captureErr != nil {
		// The screen comes back with the error: the driver returns "" on an
		// error today, and the column must not depend on that.
		return f.plain, f.captureErr
	}
	return f.plain, f.err
}

func (f *fakePane) CapturePaneEscapes(id string) (string, error) {
	f.calls = append(f.calls, "capture-e "+id)
	if f.captureErr != nil {
		// The screen comes back with the error: the driver returns "" on an
		// error today, and the column must not depend on that.
		return f.escapes, f.captureErr
	}
	return f.escapes, f.err
}

func composerFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "composer", "testdata", "claude", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runningSeat(runtime string) api.Session {
	return api.Session{Name: "s", Workspace: "ws", Team: "t", PaneID: "%7", State: api.SessionRunning, Runtime: api.Runtime{Name: runtime}}
}

// The spec's vectors, on the pure step: a spinner line reads mid_turn, a
// placeholder captured without escapes reads unknown, and an empty composer
// captured with them reads empty (the control that the reader is reached).
func TestComposerWordReadsTheFixtures(t *testing.T) {
	for name, tc := range map[string]struct {
		escapes string
		want    string
	}{
		"a spinner line is mid_turn":                        {composerFixture(t, "3a-mid-turn-1s5.ansi.txt"), "mid_turn"},
		"a placeholder captured without escapes is unknown": {composerFixture(t, "1-empty-idle.txt"), "unknown"},
		"an empty composer with escapes is empty":           {composerFixture(t, "1-empty-idle.ansi.txt"), "empty"},
	} {
		t.Run(name, func(t *testing.T) {
			p := &fakePane{foreground: "2.1.290", escapes: tc.escapes}
			if got := composerWord(p, runningSeat("claude"), false); got != tc.want {
				t.Fatalf("composer = %q, want %q", got, tc.want)
			}
			if want := []string{"foreground %7", "capture-e %7"}; !slices.Equal(p.calls, want) {
				t.Fatalf("calls = %q, want %q: the claude reader reads a capture taken with escapes", p.calls, want)
			}
		})
	}
}

// A client-workspace seat is never captured, nor is a seat whose runtime has
// no reader, a seat not running, or a pane with a shell in front.
func TestComposerWordReadsNoPaneItMayNotOrNeedNot(t *testing.T) {
	for name, tc := range map[string]struct {
		seat   api.Session
		client bool
		fg     string
		want   string
		calls  []string
	}{
		"a client workspace":       {runningSeat("claude"), true, "2.1.290", "unknown", nil},
		"a runtime with no reader": {runningSeat("opencode"), false, "opencode", "unknown", nil},
		"a seat not running": {func() api.Session {
			s := runningSeat("claude")
			s.State = api.SessionFailed
			return s
		}(), false, "2.1.290", "", nil},
		"a seat with no pane": {func() api.Session {
			s := runningSeat("claude")
			s.PaneID = ""
			return s
		}(), false, "2.1.290", "", nil},
		"a shell in front": {runningSeat("claude"), false, "zsh", "shell", []string{"foreground %7"}},
	} {
		t.Run(name, func(t *testing.T) {
			p := &fakePane{foreground: tc.fg, escapes: "never read", plain: "never read"}
			if got := composerWord(p, tc.seat, tc.client); got != tc.want {
				t.Fatalf("composer = %q, want %q", got, tc.want)
			}
			if !slices.Equal(p.calls, tc.calls) {
				t.Fatalf("calls = %q, want %q", p.calls, tc.calls)
			}
		})
	}
}

// Until a client-workspace source is ruled, no workspace is captured: the
// column reads unknown everywhere rather than open a client's panes.
func TestComposerClientSourceUnruledCapturesNothing(t *testing.T) {
	var d Daemon
	for _, ws := range []string{"", "ws", "any"} {
		if !d.composerClientWorkspace(ws) {
			t.Errorf("workspace %q may be captured with no source ruled", ws)
		}
	}
	d.composerClient = func(ws string) bool { return ws == "client-ws" }
	if d.composerClientWorkspace("ws") || !d.composerClientWorkspace("client-ws") {
		t.Error("the injected source is not the one consulted")
	}
}

// A failed read is unknown, never a guess: a failed foreground read, and a
// capture that fails after the foreground read succeeded. The second fake
// returns a mid-turn screen beside its error, so reading it anyway would give
// mid_turn, not unknown.
func TestComposerWordFailedReadIsUnknown(t *testing.T) {
	for name, p := range map[string]*fakePane{
		"the foreground read fails": {foreground: "2.1.290", err: errors.New("no such pane")},
		"the capture fails": {
			foreground: "2.1.290", captureErr: errors.New("capture-pane timed out"),
			escapes: composerFixture(t, "3a-mid-turn-1s5.ansi.txt"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := composerWord(p, runningSeat("claude"), false); got != "unknown" {
				t.Fatalf("composer = %q, want unknown", got)
			}
		})
	}
}

// The column's pane access is three reads and nothing else: no repaint, no
// resize, no keys. A method added here would let the column change a seat's
// terminal.
func TestComposerPaneIsReadOnly(t *testing.T) {
	typ := reflect.TypeFor[composerPane]()
	var got []string
	for i := range typ.NumMethod() {
		got = append(got, typ.Method(i).Name)
	}
	if want := []string{"CapturePane", "CapturePaneEscapes", "PaneForeground"}; !slices.Equal(got, want) {
		t.Fatalf("composerPane methods = %q, want only %q", got, want)
	}
}

// End to end on this package's scratch tmux server: a seat painting the
// spinner frame lists as mid_turn when the column is asked for, nothing when
// it is not, and never on the stored session. A client workspace's seat lists
// as unknown. No file under the scratch HOME holds the captured text.
func TestGetSessionsComposerColumn(t *testing.T) {
	d := newHandlerDaemon(t)
	// A claude frame with the spinner line above the composer. The spinner
	// verb is the marker for the file check: it is on the screen the column
	// reads, and it is made at run time so no build artifact holds it.
	marker := "Mark" + strconv.Itoa(os.Getpid()) + "ing"
	frame := "\n✻ " + marker + "… (1s · esc to interrupt)\n\n" + testRule + "\n❯" + testNB + "\n" + testRule + "\n  [Model] scratch\n"
	script := claudeFrameSeat(t, frame)
	key := composerSeat(t, d, "composerws", script)
	client := composerSeat(t, d, "composerclientws", script)
	d.composerClient = func(ws string) bool { return ws == "composerclientws" }

	served := func(composer bool) map[string]api.Session {
		t.Helper()
		params := map[string]any{"resource_type": "sessions"}
		if composer {
			params["composer"] = true
		}
		resp := d.handleGet(mustMarshal(t, params))
		if resp.Error != "" {
			t.Fatalf("get: %s", resp.Error)
		}
		var got []api.Session
		if err := json.Unmarshal(resp.Result, &got); err != nil {
			t.Fatal(err)
		}
		out := map[string]api.Session{}
		for _, s := range got {
			out[s.Key()] = s
		}
		return out
	}

	if s := served(false); s[key].Composer != "" || s[client].Composer != "" {
		t.Fatalf("a listing that did not ask read a pane: %q, %q", s[key].Composer, s[client].Composer)
	}
	var got map[string]api.Session
	deadline := time.Now().Add(10 * time.Second)
	for {
		got = served(true)
		if got[key].Composer == "mid_turn" || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got[key].Composer != "mid_turn" {
		t.Fatalf("composer = %q, want mid_turn", got[key].Composer)
	}
	if got[client].Composer != "unknown" {
		t.Fatalf("client workspace composer = %q, want unknown", got[client].Composer)
	}
	stored, err := d.store.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Composer != "" {
		t.Fatalf("the stored session carries %q", stored.Composer)
	}

	// Positive control: the marker is on the screen the column read.
	screen, err := d.driver.CapturePane(got[key].PaneID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(screen, marker) {
		t.Fatalf("the marker is not on the screen, so the file check proves nothing:\n%s", screen)
	}
	// Make the store write, then look at every file under the scratch HOME.
	if err := d.store.UpdateSession(key, func(s *api.Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// Every file under the scratch HOME and TMPDIR, which is everywhere this
	// daemon writes, except the frame file the seat cats: that is the input.
	files := 0
	for _, root := range []string{os.Getenv("HOME"), os.TempDir()} {
		err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if !e.Type().IsRegular() {
				return nil
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil // a socket or a file another test removed
			}
			if string(b) == frame {
				return nil
			}
			files++
			if strings.Contains(string(b), marker) {
				t.Errorf("%s holds captured text", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files == 0 {
		t.Fatal("no files under HOME or TMPDIR, so the check read nothing")
	}
}

// composerSeat applies a one-seat team in workspace ws running script,
// retags its runtime as claude, and returns its key.
func composerSeat(t *testing.T, d *Daemon, ws, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seat.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := strings.ReplaceAll(winchManifest(path), `name = "repaintws"`, `name = "`+ws+`"`)
	// Team names are cluster-wide, so each seat gets its own.
	manifest = strings.ReplaceAll(manifest, `name = "squad"`, `name = "`+ws+`-squad"`)
	if resp := applyManifest(t, d, manifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	key := ""
	for _, s := range d.store.ListSessions() {
		if s.Workspace == ws {
			key = s.Key()
		}
	}
	if key == "" {
		t.Fatal("no session")
	}
	waitCaptureHas(t, d, key, "frame-painted")
	if err := d.store.UpdateSession(key, func(s *api.Session) error { s.Runtime.Name = "claude"; return nil }); err != nil {
		t.Fatal(err)
	}
	return key
}
