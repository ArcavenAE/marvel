package tmux

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// reply is what a fake tmux prints for one show-environment call.
type reply struct {
	out, err string
	rc       int
}

// envDriver returns a driver whose tmux is a script answering show-environment
// with the session-scope reply when the call names a session (-t) and the
// global reply when it carries -g. A reply with rc -1 hangs past the bound.
func envDriver(t *testing.T, session, global reply) *Driver {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for scope, r := range map[string]reply{"s": session, "g": global} {
		write(scope+".out", r.out)
		write(scope+".err", r.err)
		write(scope+".rc", strconv.Itoa(r.rc))
	}
	script := filepath.Join(dir, "tmux")
	body := `#!/bin/sh
d=$(dirname "$0")
scope=s
for a in "$@"; do [ "$a" = "-g" ] && scope=g; done
rc=$(cat "$d/$scope.rc")
if [ "$rc" = "-1" ]; then sleep 60; fi
cat "$d/$scope.out"
cat "$d/$scope.err" >&2
exit "$rc"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Driver{binary: script, socket: "unit"}
	// A short bound only where the test wants the call to hang; elsewhere a
	// loaded host must not turn a slow script into a false unknown.
	d.SetExecTimeout(10 * time.Second)
	if session.rc == -1 || global.rc == -1 {
		d.SetExecTimeout(300 * time.Millisecond)
	}
	return d
}

const sentinel = "SENTINEL-bearer-value"

// Each exit of show-environment maps to one of three answers, and no byte tmux
// printed reaches the result or the log (V3).
func TestBearerPresenceClassifiesExit(t *testing.T) {
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	for _, tc := range []struct {
		name            string
		session, global reply
		want            BearerPresence
	}{
		{"session holds a value", reply{out: "NAME=" + sentinel + "\n"}, reply{}, BearerPresent},
		{"session removes the name", reply{out: "-NAME\n"}, reply{out: "NAME=" + sentinel + "\n"}, BearerAbsent},
		{"session sets it empty", reply{out: "NAME=\n"}, reply{out: "NAME=" + sentinel + "\n"}, BearerAbsent},
		{"session unset, global holds a value", reply{err: "unknown variable: NAME\n", rc: 1}, reply{out: "NAME=" + sentinel + "\n"}, BearerPresent},
		{"session unset, global unset", reply{err: "unknown variable: NAME\n", rc: 1}, reply{err: "unknown variable: NAME\n", rc: 1}, BearerAbsent},
		{"session unset, global removes it", reply{err: "unknown variable: NAME\n", rc: 1}, reply{out: "-NAME\n"}, BearerAbsent},
		{"session exit 1 with other text", reply{err: "no server running " + sentinel + "\n", rc: 1}, reply{out: "NAME=x\n"}, BearerUnknown},
		{"session exit 2", reply{out: "NAME=" + sentinel + "\n", err: sentinel, rc: 2}, reply{out: "NAME=x\n"}, BearerUnknown},
		{"global exit 2 after session unset", reply{err: "unknown variable: NAME\n", rc: 1}, reply{err: sentinel, rc: 2}, BearerUnknown},
		{"session hangs", reply{rc: -1}, reply{out: "NAME=x\n"}, BearerUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := envDriver(t, tc.session, tc.global)
			if got := d.BearerPresence("sess", "NAME"); got != tc.want {
				t.Errorf("presence = %v, want %v", got, tc.want)
			}
			if strings.Contains(logged.String(), sentinel) {
				t.Errorf("tmux output reached the log:\n%s", logged.String())
			}
		})
	}
}

// The unset name is the common case on a clean host: tmux exits 1 with an
// unknown-variable message, which is absence and not a failure to look.
func TestBearerPresenceUnsetNameIsAbsentNotUnknown(t *testing.T) {
	unset := reply{err: "unknown variable: ANTHROPIC_API_KEY\n", rc: 1}
	d := envDriver(t, unset, unset)
	if got := d.BearerPresence("sess", "ANTHROPIC_API_KEY"); got != BearerAbsent {
		t.Errorf("presence = %v, want absent", got)
	}
}

// A session-scope removal wins over a global value, and a session-scope
// unknown-variable falls through to the global scope.
func TestBearerPresenceSessionOverridesGlobal(t *testing.T) {
	d := envDriver(t, reply{out: "-NAME\n"}, reply{out: "NAME=" + sentinel + "\n"})
	if got := d.BearerPresence("sess", "NAME"); got != BearerAbsent {
		t.Errorf("session removal: presence = %v, want absent", got)
	}
	d = envDriver(t, reply{err: "unknown variable: NAME\n", rc: 1}, reply{out: "NAME=" + sentinel + "\n"})
	if got := d.BearerPresence("sess", "NAME"); got != BearerPresent {
		t.Errorf("fall through: presence = %v, want present", got)
	}
}
