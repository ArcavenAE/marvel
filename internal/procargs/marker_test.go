package procargs

import (
	"fmt"
	"strings"
	"testing"
)

// markerBuf builds a kern.procargs2 buffer with the existing procargs2 helper.
func markerBuf(argv, env []string) []byte {
	return procargs2(int32(len(argv)), "/usr/local/bin/tmux", 2, argv, env...)
}

var markerEnv = []string{
	"PATH=/usr/bin",
	"SECRET_X=supersecret",
	"MARVEL_HEARTBEAT_TOKEN=tok-abc123",
	"MARVEL_SESSION=aae/seat-1",
	"HOME=/Users/someone",
}

// The marker holds the MARVEL_SESSION value, and nothing else from the
// environment reaches it: not another variable, not another MARVEL_* value.
func TestParseProcargs2MarkerKeepsOnlyTheMarker(t *testing.T) {
	m, err := ParseProcargs2Marker(markerBuf([]string{"tmux", "attach"}, markerEnv))
	if err != nil {
		t.Fatalf("ParseProcargs2Marker: %v", err)
	}
	if m.Session != "aae/seat-1" {
		t.Errorf("Session = %q, want aae/seat-1", m.Session)
	}
	if m.MarvelPath {
		t.Error("MarvelPath is true with no marvel path in the environment")
	}
	shown := fmt.Sprintf("%+v %#v", m, m)
	for _, leak := range []string{"supersecret", "tok-abc123", "/usr/bin", "/Users/someone", "attach"} {
		if strings.Contains(shown, leak) {
			t.Errorf("the marker holds %q: %s", leak, shown)
		}
	}
}

// A marvel test or harness path in MARVEL_SOCKET or MARVEL_HOME is the second
// marker; a real home is not.
func TestParseProcargs2MarkerPathMarks(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{
		{"MARVEL_HOME=/tmp/marvel-test-home-123/.marvel", true},
		{"MARVEL_SOCKET=/tmp/marvel-h-ab12/s.sock", true},
		{"MARVEL_HOME=/var/folders/x/marvel-harness-homes/acme", true},
		{"MARVEL_HOME=/Users/someone/.marvel", false},
		{"MARVEL_TOKEN=/tmp/marvel-test-home-1", false},
	} {
		m, err := ParseProcargs2Marker(markerBuf([]string{"x"}, []string{"A=1", tc.env}))
		if err != nil {
			t.Fatalf("%s: %v", tc.env, err)
		}
		if m.MarvelPath != tc.want {
			t.Errorf("%s: MarvelPath = %v, want %v", tc.env, m.MarvelPath, tc.want)
		}
		if m.Marvel() != tc.want {
			t.Errorf("%s: Marvel() = %v, want %v", tc.env, m.Marvel(), tc.want)
		}
	}
}

// A process with no marvel variable has no marker, and a long MARVEL_SESSION is
// cut, so a hostile value cannot grow what the sampler keeps.
func TestParseProcargs2MarkerEmptyAndLong(t *testing.T) {
	m, err := ParseProcargs2Marker(markerBuf([]string{"x"}, []string{"PATH=/usr/bin", "HOME=/h"}))
	if err != nil || m.Marvel() || m.Session != "" {
		t.Errorf("no marvel variable: %+v, %v, want the zero marker", m, err)
	}
	long := strings.Repeat("s", 1000)
	m, err = ParseProcargs2Marker(markerBuf([]string{"x"}, []string{"MARVEL_SESSION=" + long}))
	if err != nil || len(m.Session) != MaxSessionLen {
		t.Errorf("long session: len %d, %v, want %d", len(m.Session), err, MaxSessionLen)
	}
	if !m.Marvel() {
		t.Error("a long MARVEL_SESSION is not a marvel marker")
	}
}

// A buffer that is too short, or whose argc runs past its bytes, is an error
// and not a panic, and carries no marker.
func TestParseProcargs2MarkerBadBuffers(t *testing.T) {
	for name, b := range map[string][]byte{
		"empty":     nil,
		"too short": {1, 0},
		"argc past": append([]byte{9, 0, 0, 0}, []byte("/bin/x\x00\x00a\x00")...),
	} {
		m, err := ParseProcargs2Marker(b)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if m.Marvel() {
			t.Errorf("%s: a marker %+v from a bad buffer", name, m)
		}
	}
}

// /proc/<pid>/environ is the same reduction for Linux.
func TestParseEnvironKeepsOnlyTheMarker(t *testing.T) {
	b := []byte(strings.Join(markerEnv, "\x00") + "\x00MARVEL_HOME=/tmp/marvel-test-home-9/.marvel\x00")
	m := ParseEnviron(b)
	if m.Session != "aae/seat-1" || !m.MarvelPath {
		t.Errorf("marker = %+v, want session aae/seat-1 and a marvel path", m)
	}
	shown := fmt.Sprintf("%+v", m)
	for _, leak := range []string{"supersecret", "tok-abc123", "/Users/someone"} {
		if strings.Contains(shown, leak) {
			t.Errorf("the marker holds %q: %s", leak, shown)
		}
	}
	if z := ParseEnviron(nil); z.Marvel() {
		t.Errorf("empty environ gave %+v", z)
	}
}

// Only the exact variable name counts: a name that merely contains MARVEL_SESSION
// at either end, or that extends it, sets no Session. The same environment with
// the real variable added is the positive control.
func TestParseProcargs2MarkerIgnoresLookalikeNames(t *testing.T) {
	lookalikes := []string{"MARVEL_SESSIONS=x", "XMARVEL_SESSION=x", "MARVEL_SESSION_ID=x", "marvel_session=x", "MARVEL_SESSION", "MARVEL_HOMES=/tmp/marvel-test-home-1"}
	m, err := ParseProcargs2Marker(markerBuf([]string{"x"}, lookalikes))
	if err != nil {
		t.Fatal(err)
	}
	if m.Marvel() || m.Session != "" || m.MarvelPath {
		t.Errorf("lookalike names gave %+v, want the zero marker", m)
	}
	m, err = ParseProcargs2Marker(markerBuf([]string{"x"}, append(lookalikes, "MARVEL_SESSION=real")))
	if err != nil || m.Session != "real" {
		t.Errorf("with the real variable added: %+v, %v, want Session real", m, err)
	}
}

// A MARVEL_SESSION=... string in the argument list is an argument, not the
// environment, and sets nothing.
func TestParseProcargs2MarkerIgnoresArgv(t *testing.T) {
	argv := []string{"tmux", "MARVEL_SESSION=fake", "MARVEL_HOME=/tmp/marvel-test-home-1/.marvel"}
	m, err := ParseProcargs2Marker(markerBuf(argv, []string{"PATH=/usr/bin"}))
	if err != nil {
		t.Fatal(err)
	}
	if m.Marvel() || m.Session != "" || m.MarvelPath {
		t.Errorf("argv text gave %+v, want the zero marker", m)
	}
	m, err = ParseProcargs2Marker(markerBuf(argv, []string{"PATH=/usr/bin", "MARVEL_SESSION=real"}))
	if err != nil || m.Session != "real" {
		t.Errorf("with the real variable in the environment: %+v, %v, want Session real", m, err)
	}
}

// An empty MARVEL_SESSION is no marker: a seat always has an identity, so a
// variable that is set but empty does not mark a process as marvel's. An empty
// MARVEL_HOME or MARVEL_SOCKET is no path mark either.
func TestParseProcargs2MarkerEmptyValueIsNoMarker(t *testing.T) {
	m, err := ParseProcargs2Marker(markerBuf([]string{"x"}, []string{"MARVEL_SESSION=", "MARVEL_HOME=", "MARVEL_SOCKET="}))
	if err != nil {
		t.Fatal(err)
	}
	if m.Marvel() {
		t.Errorf("empty values gave %+v, want no marker", m)
	}
	m, err = ParseProcargs2Marker(markerBuf([]string{"x"}, []string{"MARVEL_SESSION=", "MARVEL_SESSION=real"}))
	if err != nil || m.Session != "real" {
		t.Errorf("an empty then a real MARVEL_SESSION: %+v, %v, want Session real", m, err)
	}
}

// The same three rules hold for /proc/<pid>/environ.
func TestParseEnvironLookalikesAndEmpty(t *testing.T) {
	none := "MARVEL_SESSIONS=x\x00XMARVEL_SESSION=x\x00MARVEL_SESSION=\x00MARVEL_HOME=\x00"
	if m := ParseEnviron([]byte(none)); m.Marvel() {
		t.Errorf("lookalikes and empty values gave %+v, want no marker", m)
	}
	if m := ParseEnviron([]byte(none + "MARVEL_SESSION=real\x00")); m.Session != "real" {
		t.Errorf("with the real variable: %+v, want Session real", m)
	}
}

// A MARVEL_SESSION value is cut and its unprintable bytes replaced, so a hostile
// value cannot carry a terminal escape sequence into get vitals or an event.
func TestMarkerSessionIsPrintableAndBounded(t *testing.T) {
	hostile := "\x1b[31mred\x07\x00tail\xff"
	m, err := ParseProcargs2Marker(markerBuf([]string{"x"}, []string{"MARVEL_SESSION=" + hostile}))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []byte(m.Session) {
		if c < 0x20 || c > 0x7e {
			t.Errorf("session %q holds the byte %#x, want printable ASCII only", m.Session, c)
		}
	}
	if !strings.Contains(m.Session, "red") {
		t.Errorf("session = %q, want the printable text kept", m.Session)
	}
}
