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
