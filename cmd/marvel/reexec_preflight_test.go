package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/upgrade"
)

// rig stands in for the daemon and the process table: the build query, the
// argv read and the reexec send, in the order they are called.
type rig struct {
	trace    []string
	pid      int
	queryErr error
	argv     []string
	argvErr  error
	sent     []daemon.Request
}

func newRig(t *testing.T, argv ...string) *rig {
	t.Helper()
	r := &rig{pid: 4242, argv: argv}
	withSocket(t, filepath.Join(t.TempDir(), "marvel.sock"))
	pq, pa, ps := preflightQuery, daemonArgs, reexecSend
	preflightQuery = func() (*daemon.BuildInfo, error) {
		r.trace = append(r.trace, "version")
		if r.queryErr != nil {
			return nil, r.queryErr
		}
		return &daemon.BuildInfo{PID: r.pid}, nil
	}
	daemonArgs = func(pid int) ([]string, error) {
		r.trace = append(r.trace, "argv")
		if pid != r.pid {
			t.Errorf("argv read for pid %d, want the daemon's %d", pid, r.pid)
		}
		return r.argv, r.argvErr
	}
	reexecSend = func(req daemon.Request) (*daemon.Response, error) {
		r.trace = append(r.trace, "send")
		r.sent = append(r.sent, req)
		b, _ := json.Marshal(map[string]string{"status": "reexec", "binary": "x"})
		return &daemon.Response{Result: b}, nil
	}
	t.Cleanup(func() { preflightQuery, daemonArgs, reexecSend = pq, pa, ps })
	return r
}

func runReexecCmd(t *testing.T) error {
	t.Helper()
	cmd := daemonReexecCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	return cmd.Execute()
}

const pointer = "marvel stop --keep-bus"

// T1: a daemon started with a word the new build rejects is named and refused,
// and nothing is sent, so the old daemon keeps serving.
func TestReexecRefusesADaemonStartedWithAWordThisBuildRejects(t *testing.T) {
	r := newRig(t, "/opt/marvel/marvel", "daemon", "zzz-bogus-control")
	err := runReexecCmd(t)
	if err == nil {
		t.Fatal("reexec was accepted for a daemon this build's marvel daemon would reject")
	}
	for _, want := range []string{"zzz-bogus-control", "daemon zzz-bogus-control", "rejects", pointer, "adopt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should carry %q, got %v", want, err)
		}
	}
	if len(r.sent) != 0 {
		t.Fatalf("a reexec was sent despite the refusal: %v", r.sent)
	}
}

// T2: the daemon's own usual arguments pass, and the request goes out.
func TestReexecPreflightPassesWhatTheDaemonCommandAccepts(t *testing.T) {
	for _, argv := range [][]string{
		{"/opt/marvel/marvel", "daemon"},
		{"/opt/marvel/marvel", "daemon", "--mrvl"},
		{"/opt/marvel/marvel", "daemon", "--mrvl=:7000", "--socket", "/run/m.sock", "--reclaim"},
	} {
		r := newRig(t, argv...)
		if err := runReexecCmd(t); err != nil {
			t.Errorf("%v: %v", argv, err)
		}
		if len(r.sent) != 1 || r.sent[0].Method != "reexec" {
			t.Errorf("%v: want one reexec sent, got %v", argv, r.sent)
		}
	}
}

// The parse is the real command's: an unknown flag is refused like an unknown
// word, which a hand-copied word list would miss.
func TestReexecPreflightRefusesAnUnknownFlag(t *testing.T) {
	r := newRig(t, "/opt/marvel/marvel", "daemon", "--removed-flag")
	err := runReexecCmd(t)
	if err == nil || !strings.Contains(err.Error(), "--removed-flag") || !strings.Contains(err.Error(), pointer) {
		t.Fatalf("want a refusal naming --removed-flag and the pointer, got %v", err)
	}
	if len(r.sent) != 0 {
		t.Fatalf("a reexec was sent: %v", r.sent)
	}
}

// T3: upgrade --daemon takes the same pre-flight, after the install.
func TestUpgradeDaemonTakesThePreflight(t *testing.T) {
	r := newRig(t, "/opt/marvel/marvel", "daemon", "zzz-bogus-control")
	prevV, prevU := version, runUpgrade
	version = "0.3.0"
	runUpgrade = func(string, string) (upgrade.Result, error) { return upgrade.Result{Changed: true}, nil }
	t.Cleanup(func() { version, runUpgrade = prevV, prevU })
	if err := upgrade.ReexecRefusal(); err != nil {
		t.Skipf("this host refuses --daemon before the pre-flight: %v", err)
	}
	cmd := upgradeCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--daemon"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "zzz-bogus-control") || !strings.Contains(err.Error(), pointer) {
		t.Fatalf("want the pre-flight refusal from upgrade --daemon, got %v", err)
	}
	if len(r.sent) != 0 {
		t.Fatalf("a reexec was sent: %v", r.sent)
	}
}

// T4: what cannot be read is not guessed at.
func TestReexecPreflightRefusesWhatItCannotRead(t *testing.T) {
	cases := map[string]func(*rig){
		"the version query fails":     func(r *rig) { r.queryErr = errors.New("daemon did not answer within 2s") },
		"the daemon predates version": func(r *rig) { r.queryErr = errDaemonPredates },
		"the pid has no argv":         func(r *rig) { r.argvErr = errors.New("ps: no such process") },
		"the argv is empty":           func(r *rig) { r.argv = nil },
		"the argv has no daemon word": func(r *rig) { r.argv = []string{"/opt/marvel/marvel", "version"} },
	}
	for name, mutate := range cases {
		r := newRig(t, "/opt/marvel/marvel", "daemon")
		mutate(r)
		err := runReexecCmd(t)
		if err == nil || !strings.Contains(err.Error(), pointer) {
			t.Errorf("%s: want a refusal with the pointer, got %v", name, err)
		}
		if len(r.sent) != 0 {
			t.Errorf("%s: a reexec was sent", name)
		}
	}
}

// T5: with an exec_path in the request the pre-flight still comes first.
func TestReexecPreflightRunsBeforeTheRequestThatNamesAPath(t *testing.T) {
	r := newRig(t, "/opt/marvel/marvel", "daemon")
	if err := runReexecCmd(t); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.trace, ","); got != "version,argv,send" {
		t.Fatalf("order = %s, want version,argv,send", got)
	}
	var p map[string]string
	if err := json.Unmarshal(r.sent[0].Params, &p); err != nil || p["exec_path"] == "" {
		t.Fatalf("the request should name an exec_path (%v): %s", err, r.sent[0].Params)
	}
}

// A remote daemon's pid is on another host, so there is nothing local to read.
func TestReexecPreflightSkipsARemoteDaemon(t *testing.T) {
	r := newRig(t, "/opt/marvel/marvel", "daemon", "zzz-bogus-control")
	withSocket(t, "mrvl://desk")
	if err := runReexecCmd(t); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.trace, ","); got != "send" {
		t.Fatalf("order = %s, want only send for a remote daemon", got)
	}
}

// A remote daemon is not checked, and the operator is told so before the
// request goes out.
func TestReexecRemoteSaysItSkippedThePreflightAndStillSends(t *testing.T) {
	r := newRig(t, "/opt/marvel/marvel", "daemon")
	withSocket(t, "mrvl://desk")
	cmd := daemonReexecCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var errOut bytes.Buffer
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pre-flight skipped: mrvl://desk is remote", "cannot be read from here", "will not come back", "Check them on that host first"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("the skip line should carry %q, got %q", want, errOut.String())
		}
	}
	if got := strings.Join(r.trace, ","); got != "send" {
		t.Fatalf("order = %s, want the request sent after the line", got)
	}
}

// The argument list is read exactly: an empty argument and one holding a space
// survive, which a flattened `ps` line loses.
func TestParseCmdlineKeepsEmptyAndSpacedArguments(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"plain", "marvel\x00daemon\x00--mrvl\x00", []string{"marvel", "daemon", "--mrvl"}},
		{"an empty argument", "marvel\x00daemon\x00--mrvl\x00\x00", []string{"marvel", "daemon", "--mrvl", ""}},
		{"an empty argument in the middle", "marvel\x00daemon\x00--state-bolt\x00\x00--reclaim\x00", []string{"marvel", "daemon", "--state-bolt", "", "--reclaim"}},
		{"a space inside an argument", "/opt/My Tools/marvel\x00daemon\x00--socket\x00/run/my dir/m.sock\x00", []string{"/opt/My Tools/marvel", "daemon", "--socket", "/run/my dir/m.sock"}},
	}
	for _, c := range cases {
		got, err := parseCmdline([]byte(c.in))
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q (%v), want %q", c.name, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "\x00", "marvel\x00daemon"} {
		if got, err := parseCmdline([]byte(bad)); err == nil {
			t.Errorf("%q: a process with no arguments is unreadable, got %q", bad, got)
		}
	}
}

// procargs2 is argc as a native int32, the executable path, NUL padding, then
// argc NUL-terminated arguments, then the environment, which is not read.
func procargs2(argc int32, exec string, pad int, args []string, env ...string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(argc))
	b = append(b, exec...)
	b = append(b, make([]byte, 1+pad)...)
	for _, a := range args {
		b = append(append(b, a...), 0)
	}
	for _, e := range env {
		b = append(append(b, e...), 0)
	}
	return b
}

func TestParseProcargs2KeepsEmptyAndSpacedArguments(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want []string
	}{
		{"plain", procargs2(2, "/opt/marvel", 3, []string{"marvel", "daemon"}, "HOME=/h"), []string{"marvel", "daemon"}},
		{"an empty argument", procargs2(4, "/opt/marvel", 0, []string{"marvel", "daemon", "--mrvl", ""}, "HOME=/h"), []string{"marvel", "daemon", "--mrvl", ""}},
		{"a space inside an argument", procargs2(3, "/opt/My Tools/marvel", 5, []string{"/opt/My Tools/marvel", "daemon", "--socket"}, "A=b"), []string{"/opt/My Tools/marvel", "daemon", "--socket"}},
		{"the environment is not read", procargs2(1, "/m", 1, []string{"marvel"}, "X=1", "", "Y=2"), []string{"marvel"}},
	}
	for _, c := range cases {
		got, err := parseProcargs2(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q (%v), want %q", c.name, got, err, c.want)
		}
	}
	for name, bad := range map[string][]byte{
		"too short":              {1, 0},
		"argc larger than given": procargs2(5, "/m", 0, []string{"marvel", "daemon"}),
		"no arguments":           procargs2(0, "/m", 0, nil),
		"negative argc":          procargs2(-1, "/m", 0, []string{"marvel"}),
		"an unterminated last":   append(procargs2(2, "/m", 0, []string{"marvel"}), "daemon"...),
	} {
		if got, err := parseProcargs2(bad); err == nil {
			t.Errorf("%s: want an error, got %q", name, got)
		}
	}
}

// `daemon --mrvl ""` flattens to `daemon --mrvl` under ps and passes. Read
// exactly, the empty word is a stray argument and the daemon is refused.
func TestReexecPreflightRefusesAnEmptyArgumentTheExactReadKeeps(t *testing.T) {
	r := newRig(t)
	daemonArgs = func(int) ([]string, error) {
		return parseCmdline([]byte("/opt/marvel/marvel\x00daemon\x00--mrvl\x00\x00"))
	}
	err := runReexecCmd(t)
	if err == nil || !strings.Contains(err.Error(), "rejects") || !strings.Contains(err.Error(), pointer) {
		t.Fatalf("want a refusal of `daemon --mrvl \"\"` with the pointer, got %v", err)
	}
	if len(r.sent) != 0 {
		t.Fatalf("a reexec was sent: %v", r.sent)
	}
	daemonArgs = func(int) ([]string, error) {
		return parseCmdline([]byte("/opt/marvel/marvel\x00daemon\x00--mrvl\x00"))
	}
	if err := runReexecCmd(t); err != nil {
		t.Fatalf("`daemon --mrvl` is the control and must pass: %v", err)
	}
}

// The live reader returns what the process was started with, from the kernel.
func TestReadProcessArgsReadsThisProcessExactly(t *testing.T) {
	got, err := readProcessArgs(os.Getpid())
	if err != nil {
		t.Fatalf("read own arguments: %v", err)
	}
	if !slices.Equal(got, os.Args) {
		t.Fatalf("read %q, started with %q", got, os.Args)
	}
	// The pre-flight's own source is this reader, not a flattened line.
	if wired, err := daemonArgs(os.Getpid()); err != nil || !slices.Equal(wired, os.Args) {
		t.Fatalf("the pre-flight reads %q (%v), want this process's %q", wired, err, os.Args)
	}
	if _, err := readProcessArgs(1 << 30); err == nil {
		t.Error("a pid that does not exist must be an error, not an empty list")
	}
}
