package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Section 1 of the reexec credential handoff (aae-orc-4h9xp, marvel#339): the
// departing daemon carries its transient credentials to the successor across
// syscall.Exec through a pipe, in memory only. Every credential here is a dummy.

const handoffCanary = "SUACANARYCANARYCANARYCANARYCANARYCANARYCANARYCANARYCANARYCANARY"

func dummyCred(name, value string) api.Credential {
	return api.Credential{
		Name: name, Kind: api.CredentialNATSNKeySeed, Audience: "hub", Binding: "grant-a",
		IssuedAt: time.Date(2026, 10, 15, 1, 2, 3, 0, time.UTC), IssuedBy: "SHA256:issuer", Value: []byte(value),
	}
}

func putDummy(t *testing.T, d *Daemon, name, value string) {
	t.Helper()
	c := dummyCred(name, value)
	if err := d.store.CreateCredential(&c); err != nil {
		t.Fatal(err)
	}
}

// openFDs is the set of descriptors open in this process, found by asking the
// kernel about each number. A leak shows as a number that was not there before.
func openFDs(t *testing.T) map[int]bool {
	t.Helper()
	open := map[int]bool{}
	for fd := 0; fd < 512; fd++ {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			open[fd] = true
		}
	}
	return open
}

func sameFDs(t *testing.T, before map[int]bool, what string) {
	t.Helper()
	after := openFDs(t)
	for fd := range after {
		if !before[fd] {
			t.Errorf("%s: descriptor %d was left open", what, fd)
		}
	}
}

func isClosed(fd int) bool {
	_, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	return errors.Is(err, unix.EBADF)
}

func fdFromEnv(t *testing.T, envv []string) int {
	t.Helper()
	var got []string
	for _, kv := range envv {
		if strings.HasPrefix(kv, handoffEnvVar+"=") {
			got = append(got, kv)
		}
	}
	if len(got) != 1 {
		t.Fatalf("envv carries %d %s entries, want exactly 1: %v", len(got), handoffEnvVar, got)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(got[0], handoffEnvVar+"="))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func hasHandoffVar(envv []string) bool {
	for _, kv := range envv {
		if strings.HasPrefix(kv, handoffEnvVar+"=") {
			return true
		}
	}
	return false
}

// reexecSeam replaces the exec with a recorder. The process image stays, so
// the descriptor the exec would have inherited stays open for the test to read.
type reexecSeam struct {
	called bool
	envv   []string
	err    error
}

func (d *Daemon) seamReexec(r *reexecSeam) {
	d.reexec = func(argv0 string, argv []string, envv []string) error {
		r.called, r.envv = true, slices.Clone(envv)
		return r.err
	}
}

func capableTarget(string) error { return nil }

func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	b := &lockedBuffer{}
	prev := log.Writer()
	log.SetOutput(b)
	t.Cleanup(func() { log.SetOutput(prev) })
	return b
}

// lockedBuffer is a log sink a test can read while a goroutine it started is
// still logging. log serializes its own writes, but not a read of the buffer.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// --- the carry table ---------------------------------------------------------

func TestEveryCredentialKindHasAWrittenCarryDecision(t *testing.T) {
	for _, k := range api.CredentialKinds() {
		if _, ok := carryTable[k]; !ok {
			t.Errorf("credential kind %q is not in the carry table: it would travel or drop without a decision", k)
		}
	}
	if !carryTable[api.CredentialNATSNKeySeed] {
		t.Error("the nats-nkey-seed kind must be carried: it is the one that keeps the hub leaf up")
	}
}

// --- the wire format ---------------------------------------------------------

func TestHandoffPayloadRoundTripsEveryField(t *testing.T) {
	in := []api.Credential{dummyCred("bus/leaf", "dummy-one"), dummyCred("bus/hub", "dummy-two")}
	buf, err := encodeHandoff(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeHandoff(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("decoded %d credentials, want 2", len(out))
	}
	for i := range in {
		a, b := in[i], out[i]
		if a.Name != b.Name || a.Kind != b.Kind || a.Audience != b.Audience || a.Binding != b.Binding ||
			!a.IssuedAt.Equal(b.IssuedAt) || a.IssuedBy != b.IssuedBy || !bytes.Equal(a.Value, b.Value) {
			t.Errorf("credential %d: got %+v, want %+v", i, b, a)
		}
		if b.Persist {
			t.Errorf("credential %d came back persistent", i)
		}
	}
}

func TestHandoffPayloadRefusesWhatIsNotWhole(t *testing.T) {
	good, err := encodeHandoff([]api.Credential{dummyCred("bus/leaf", "dummy-one")})
	if err != nil {
		t.Fatal(err)
	}
	wrongVersion := slices.Clone(good)
	wrongVersion[4]++
	badMagic := slices.Clone(good)
	badMagic[0] ^= 0xff
	cases := map[string][]byte{
		"empty":            nil,
		"short":            good[:len(good)-3],
		"header only":      good[:6],
		"garbled":          bytes.Repeat([]byte{0xde, 0xad}, 40),
		"wrong version":    wrongVersion,
		"wrong magic":      badMagic,
		"trailing bytes":   append(slices.Clone(good), 1, 2, 3),
		"oversized":        bytes.Repeat([]byte{0}, handoffCap+1),
		"a count too high": append(slices.Clone(good[:5]), 0xff, 0xff),
	}
	for name, buf := range cases {
		if out, err := decodeHandoff(buf); err == nil {
			t.Errorf("%s: decoded %+v, want an error and no partial credential", name, out)
		} else if out != nil {
			t.Errorf("%s: returned %d credentials beside the error", name, len(out))
		}
	}
}

func TestHandoffRefusesAKindItDoesNotKnowOnTheWayIn(t *testing.T) {
	c := dummyCred("x", "v")
	c.Kind = "something-new"
	buf, err := encodeHandoff([]api.Credential{c})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := decodeHandoff(buf); err == nil {
		t.Errorf("decoded a credential of an unknown kind: %+v", out)
	}
}

// --- the guard (1.1) ---------------------------------------------------------

func TestProbeAcceptsATargetThatSaysItCanReadTheHandoff(t *testing.T) {
	exe := scriptTarget(t, `echo '{"reexec_handoff":1}'`)
	// Production allows 2s; this positive case is given room, since a loaded race
	// build can take longer than that to start a shell script.
	if err := probeReexecTarget(exe, 30*time.Second); err != nil {
		t.Errorf("a capable target was declined: %v", err)
	}
}

func TestProbeDeclinesEverythingElse(t *testing.T) {
	cases := map[string]string{
		"an older binary that rejects the flag": `echo "unknown flag: --capabilities" >&2; exit 1`,
		"the capability absent":                 `echo '{}'`,
		"the capability zero":                   `echo '{"reexec_handoff":0}'`,
		"a newer format it was not told about":  `echo '{"reexec_handoff":2}'`,
		"not json":                              `echo hello`,
		"silence":                               `exit 0`,
		"a hang":                                `sleep 5`,
	}
	for name, body := range cases {
		start := time.Now()
		if err := probeReexecTarget(scriptTarget(t, body), 300*time.Millisecond); err == nil {
			t.Errorf("%s: the target was accepted", name)
		}
		if time.Since(start) > 3*time.Second {
			t.Errorf("%s: the probe took %s, want it bounded by its timeout", name, time.Since(start))
		}
	}
	if err := probeReexecTarget(filepath.Join(t.TempDir(), "absent"), time.Second); err == nil {
		t.Error("a target that does not exist was accepted")
	}
}

// scriptTarget is a stand-in target binary: a shell script that answers the
// probe the way the body says.
func scriptTarget(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "marvel-target")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// --- the departing side (1.1 to 1.4) -----------------------------------------

func TestACarriedReexecPassesOneFDNumberAndNeverTheSeed(t *testing.T) {
	d := newHandlerDaemon(t)
	putDummy(t, d, "bus/leaf", handoffCanary)
	d.handoffProbe = capableTarget
	var r reexecSeam
	d.seamReexec(&r)
	logs := captureLog(t)
	before := openFDs(t)

	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	if !r.called {
		t.Fatal("the exec seam was not called")
	}
	fd := fdFromEnv(t, r.envv)
	for _, kv := range r.envv {
		if strings.Contains(kv, handoffCanary) {
			t.Errorf("envv carries the seed: %q", kv)
		}
	}
	if strings.Contains(logs.String(), handoffCanary) {
		t.Error("the log carries the seed")
	}
	if !strings.Contains(logs.String(), "carried 1") {
		t.Errorf("the log says nothing about the carry:\n%s", logs.String())
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC != 0 {
		t.Errorf("the read end must survive the exec: flags=%d err=%v", flags, err)
	}

	// The successor reads it and the descriptor goes away.
	t.Setenv(handoffEnvVar, strconv.Itoa(fd))
	creds, got := receiveReexecHandoff()
	if got.State != handoffRead {
		t.Fatalf("read state = %v (%v), want ok", got.State, got.Err)
	}
	if len(creds) != 1 || creds[0].Name != "bus/leaf" || string(creds[0].Value) != handoffCanary {
		t.Errorf("received %+v, want the one credential that was put", creds)
	}
	if !isClosed(fd) {
		t.Error("the handoff descriptor is still open after the read")
	}
	if _, set := os.LookupEnv(handoffEnvVar); set {
		t.Error("the successor left the handoff variable in its environment, where a child would inherit it")
	}
	sameFDs(t, before, "a carried reexec after the successor's read")
}

func TestADeclinedHandoffCreatesNothingAndSaysWhy(t *testing.T) {
	d := newHandlerDaemon(t)
	putDummy(t, d, "bus/leaf", handoffCanary)
	d.handoffProbe = func(string) error { return errors.New("exit status 1") }
	var r reexecSeam
	d.seamReexec(&r)
	logs := captureLog(t)
	before := openFDs(t)

	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	if hasHandoffVar(r.envv) {
		t.Error("a declined handoff still set the variable")
	}
	sameFDs(t, before, "a declined handoff")
	if !strings.Contains(logs.String(), "dropped 1, target cannot read the handoff") {
		t.Errorf("the log does not name the decline:\n%s", logs.String())
	}
}

func TestAPayloadOverTheCapCarriesNothingAndSaysSo(t *testing.T) {
	d := newHandlerDaemon(t)
	putDummy(t, d, "bus/leaf", strings.Repeat("x", handoffCap+10))
	d.handoffProbe = capableTarget
	var r reexecSeam
	d.seamReexec(&r)
	logs := captureLog(t)
	before := openFDs(t)

	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	if hasHandoffVar(r.envv) {
		t.Error("an oversized payload still set the variable")
	}
	sameFDs(t, before, "an oversized payload")
	if !strings.Contains(logs.String(), "dropped 1, payload over cap") {
		t.Errorf("the log does not name the cap:\n%s", logs.String())
	}
}

func TestNothingToCarryMeansNoProbeAndNoPipe(t *testing.T) {
	d := newHandlerDaemon(t)
	probed := false
	d.handoffProbe = func(string) error { probed = true; return nil }
	var r reexecSeam
	d.seamReexec(&r)
	before := openFDs(t)
	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	if probed || hasHandoffVar(r.envv) {
		t.Errorf("a daemon with no credentials probed (%v) or set the variable (%v)", probed, hasHandoffVar(r.envv))
	}
	sameFDs(t, before, "a reexec with nothing to carry")
}

func TestAnExecErrorClosesAndZeroesBothEnds(t *testing.T) {
	d := newHandlerDaemon(t)
	putDummy(t, d, "bus/leaf", handoffCanary)
	d.handoffProbe = capableTarget
	r := reexecSeam{err: errors.New("exec: permission denied")}
	d.seamReexec(&r)
	before := openFDs(t)

	if err := d.ReexecTo("/bin/marvel-target"); err == nil {
		t.Fatal("ReexecTo swallowed the exec error")
	}
	sameFDs(t, before, "an exec that returned an error")
	if h := d.lastHandoff; h == nil {
		t.Fatal("no handoff recorded for inspection")
	} else if !h.wiped() {
		t.Error("the payload buffer still holds bytes after the failed exec")
	}
}

func TestThePayloadBufferIsZeroedOnceItIsWritten(t *testing.T) {
	d := newHandlerDaemon(t)
	putDummy(t, d, "bus/leaf", handoffCanary)
	d.handoffProbe = capableTarget
	h := d.prepareHandoff("/bin/marvel-target")
	defer h.abort()
	if h.Outcome() != "carried" {
		t.Fatalf("outcome = %q, want carried", h.Outcome())
	}
	if !h.wiped() {
		t.Error("the buffer still holds the seed after the write")
	}
}

func TestADoubleReexecHandsOnlyTheFreshFDNumber(t *testing.T) {
	d := newHandlerDaemon(t)
	putDummy(t, d, "bus/leaf", handoffCanary)
	d.handoffProbe = capableTarget
	var r reexecSeam
	d.seamReexec(&r)
	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	first := fdFromEnv(t, r.envv)

	// Hop two: the successor read the first handoff and unset the variable,
	// then reexecs again with its own credentials.
	t.Setenv(handoffEnvVar, strconv.Itoa(first))
	creds, got := receiveReexecHandoff()
	if got.State != handoffRead || len(creds) != 1 {
		t.Fatalf("hop one read: %v %v", got.State, got.Err)
	}
	d2 := newHandlerDaemon(t)
	for i := range creds {
		if err := d2.store.CreateCredential(&creds[i]); err != nil {
			t.Fatal(err)
		}
	}
	d2.handoffProbe = capableTarget
	var r2 reexecSeam
	d2.seamReexec(&r2)
	// A stale variable in the environment must not ride along either.
	t.Setenv(handoffEnvVar, "999")
	if err := d2.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	second := fdFromEnv(t, r2.envv)
	if second == 999 {
		t.Error("the second hop passed on the stale variable instead of its own descriptor")
	}
	t.Setenv(handoffEnvVar, strconv.Itoa(second))
	if creds, got := receiveReexecHandoff(); got.State != handoffRead || len(creds) != 1 || string(creds[0].Value) != handoffCanary {
		t.Errorf("hop two read: %v %v %+v", got.State, got.Err, creds)
	}
}

// --- the receiving side (1.5) ------------------------------------------------

func TestNoVariableReadsAsAbsentAndTouchesNothing(t *testing.T) {
	_ = os.Unsetenv(handoffEnvVar)
	before := openFDs(t)
	creds, got := receiveReexecHandoff()
	if got.State != handoffAbsent || creds != nil {
		t.Errorf("state = %v, creds = %v, want absent and none", got.State, creds)
	}
	sameFDs(t, before, "a start with no handoff")
}

func TestAHandoffThatCannotBeReadIsUnreadAndNeverPartial(t *testing.T) {
	garbled := func(t *testing.T) int {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(bytes.Repeat([]byte{7}, 40))
		_ = w.Close()
		// Hold the read end open past this helper, as an exec would.
		fd, _ := unix.FcntlInt(r.Fd(), unix.F_DUPFD, 100)
		_ = r.Close()
		return fd
	}
	t.Run("garbled", func(t *testing.T) {
		fd := garbled(t)
		t.Setenv(handoffEnvVar, strconv.Itoa(fd))
		creds, got := receiveReexecHandoff()
		if got.State != handoffUnread || creds != nil {
			t.Errorf("state = %v, creds = %v, want unread and none", got.State, creds)
		}
		if !isClosed(fd) {
			t.Error("a FIFO that was read must be closed")
		}
	})
	t.Run("a descriptor that is not open", func(t *testing.T) {
		t.Setenv(handoffEnvVar, "407")
		if _, got := receiveReexecHandoff(); got.State != handoffUnread {
			t.Errorf("state = %v, want unread", got.State)
		}
	})
	t.Run("not a number", func(t *testing.T) {
		t.Setenv(handoffEnvVar, "seven")
		if _, got := receiveReexecHandoff(); got.State != handoffUnread {
			t.Errorf("state = %v, want unread", got.State)
		}
	})
	t.Run("a standard descriptor", func(t *testing.T) {
		t.Setenv(handoffEnvVar, "1")
		if _, got := receiveReexecHandoff(); got.State != handoffUnread {
			t.Errorf("state = %v, want unread", got.State)
		}
		if isClosed(1) {
			t.Error("the successor closed standard output")
		}
	})
	t.Run("an open descriptor that is not a pipe", func(t *testing.T) {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		t.Setenv(handoffEnvVar, strconv.Itoa(int(f.Fd())))
		if _, got := receiveReexecHandoff(); got.State != handoffUnread {
			t.Errorf("state = %v, want unread", got.State)
		}
		if isClosed(int(f.Fd())) {
			t.Error("the successor closed a descriptor that is not its handoff pipe")
		}
	})
}

// --- the reexec response (architect addition) ---------------------------------

func TestTheReexecResponseNamesTheHandoffBeforeTheExec(t *testing.T) {
	cases := []struct {
		name  string
		setup func(d *Daemon)
		want  string
	}{
		{"carried", func(d *Daemon) { putDummy(t, d, "bus/leaf", handoffCanary); d.handoffProbe = capableTarget }, "carried"},
		{"declined", func(d *Daemon) {
			putDummy(t, d, "bus/leaf", handoffCanary)
			d.handoffProbe = func(string) error { return errors.New("no") }
		}, "declined"},
		{"over cap", func(d *Daemon) {
			putDummy(t, d, "bus/leaf", strings.Repeat("x", handoffCap+1))
			d.handoffProbe = capableTarget
		}, "over-cap"},
		{"nothing to carry", func(d *Daemon) { d.handoffProbe = capableTarget }, "none"},
	}
	for _, c := range cases {
		d := newHandlerDaemon(t)
		c.setup(d)
		done := make(chan []string, 1)
		d.reexec = func(argv0 string, argv []string, envv []string) error {
			done <- slices.Clone(envv)
			return nil
		}
		resp := d.handleReexec(nil, localCaller())
		if resp.Error != "" {
			t.Fatalf("%s: %s", c.name, resp.Error)
		}
		var r struct {
			Handoff string `json:"handoff"`
		}
		if err := json.Unmarshal(resp.Result, &r); err != nil {
			t.Fatal(err)
		}
		if r.Handoff != c.want {
			t.Errorf("%s: response handoff = %q, want %q", c.name, r.Handoff, c.want)
		}
		select {
		case envv := <-done:
			if c.want == "carried" {
				fd := fdFromEnv(t, envv)
				_ = unix.Close(fd)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the exec seam was never called", c.name)
		}
	}
}

// --- the dropped count (1.3) -------------------------------------------------

func TestACarriedHandoffRaisesNoDroppedEventButADeclinedOneDoes(t *testing.T) {
	dropped := func(d *Daemon) int {
		return len(d.events.Snapshot(events.Filter{Kind: events.KindCredentialTransientDropped}, 0))
	}
	d := newHandlerDaemon(t)
	putDummy(t, d, "bus/leaf", handoffCanary)
	d.handoffProbe = capableTarget
	var r reexecSeam
	d.seamReexec(&r)
	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	_ = unix.Close(fdFromEnv(t, r.envv))
	if n := dropped(d); n != 0 {
		t.Errorf("a carried handoff raised %d dropped events, want 0", n)
	}

	d2 := newHandlerDaemon(t)
	putDummy(t, d2, "bus/leaf", handoffCanary)
	d2.handoffProbe = func(string) error { return errors.New("no") }
	var r2 reexecSeam
	d2.seamReexec(&r2)
	if err := d2.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	if n := dropped(d2); n != 1 {
		t.Errorf("a declined handoff raised %d dropped events, want 1", n)
	}
}

func TestStopAndTeardownStillSayDropped(t *testing.T) {
	d := newHandlerDaemon(t)
	putDummy(t, d, "bus/leaf", handoffCanary)
	logs := captureLog(t)
	d.Detach()
	if !strings.Contains(logs.String(), "1 transient credential(s) dropped") {
		t.Errorf("a plain detach must keep saying dropped:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), handoffCanary) {
		t.Error("the log carries the seed")
	}
}
