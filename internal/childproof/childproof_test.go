package childproof

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeKernel is the test's view of the process table: one identity per pid,
// changeable between calls, and a record of every signal sent.
type fakeKernel struct {
	mu    sync.Mutex
	ids   map[int]Identity
	sent  []sent
	reads int
}

type sent struct {
	pid int
	sig syscall.Signal
}

func newKernel(ids map[int]Identity) *fakeKernel { return &fakeKernel{ids: ids} }

func (k *fakeKernel) read(pid int) (Identity, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.reads++
	id, ok := k.ids[pid]
	if !ok {
		return Identity{}, syscall.ESRCH
	}
	return id, nil
}

func (k *fakeKernel) kill(pid int, sig syscall.Signal) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sent = append(k.sent, sent{pid, sig})
	return nil
}

func (k *fakeKernel) set(pid int, id Identity) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ids[pid] = id
}

func (k *fakeKernel) signals() []sent {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.sent)
}

func broker(start string) Identity {
	return Identity{Start: start, Exe: "/opt/bin/nats-server", Argv: []string{"/opt/bin/nats-server", "-c", "/run/nats.conf"}, Pgid: 500}
}

func prober(t *testing.T, k *fakeKernel) *Prober {
	t.Helper()
	p, err := NewForTest(k.read, k.kill)
	if err != nil {
		t.Fatalf("NewForTest in a test binary: %v", err)
	}
	return p
}

func TestNewForTestRefusesANilSeam(t *testing.T) {
	k := newKernel(nil)
	if _, err := NewForTest(nil, k.kill); err == nil {
		t.Error("a nil read seam was accepted")
	}
	if _, err := NewForTest(k.read, nil); err == nil {
		t.Error("a nil kill seam was accepted: a test could reach the real kill by leaving it unset")
	}
	if _, err := NewForTest(k.read, k.kill); err != nil {
		t.Errorf("two seams in a test binary: %v", err)
	}
}

func TestProveReturnsAChildOnlyForAnExactMatch(t *testing.T) {
	want := broker("darwin:100.5")
	for name, tc := range map[string]struct {
		live   Identity
		absent bool
		reason string
	}{
		"exact":               {live: want},
		"start differs":       {live: broker("darwin:100.6"), reason: "start"},
		"exe differs":         {live: Identity{Start: want.Start, Exe: "/bin/sleep", Argv: want.Argv, Pgid: 500}, reason: "exe"},
		"argv differs":        {live: Identity{Start: want.Start, Exe: want.Exe, Argv: []string{want.Exe, "-c", "/other"}, Pgid: 500}, reason: "argv"},
		"argv is shorter":     {live: Identity{Start: want.Start, Exe: want.Exe, Argv: want.Argv[:2], Pgid: 500}, reason: "argv"},
		"the process is gone": {absent: true, reason: "dead"},
	} {
		k := newKernel(map[int]Identity{})
		if !tc.absent {
			k.set(41, tc.live)
		}
		c, err := prober(t, k).Prove(41, want)
		if tc.reason == "" {
			if err != nil || c.Pid() != 41 || c.Identity().Start != want.Start {
				t.Errorf("%s: Prove = %+v, %v; want a Child for pid 41", name, c, err)
			}
			continue
		}
		var m *MismatchError
		if !errors.As(err, &m) || m.Reason != tc.reason {
			t.Errorf("%s: Prove error = %v, want a MismatchError with reason %q", name, err, tc.reason)
		}
		if c.Pid() != 0 {
			t.Errorf("%s: a refused proof still returned pid %d", name, c.Pid())
		}
		if got := k.signals(); len(got) != 0 {
			t.Errorf("%s: proving sent signals %v", name, got)
		}
	}
}

func TestProveRefusesWhatCannotBeAProcess(t *testing.T) {
	k := newKernel(map[int]Identity{0: broker("a"), 1: broker("a"), 41: broker("a")})
	p := prober(t, k)
	for _, pid := range []int{-5, 0, 1} {
		if _, err := p.Prove(pid, broker("a")); err == nil {
			t.Errorf("Prove(%d) succeeded", pid)
		}
	}
	if _, err := p.Prove(41, Identity{}); err == nil {
		t.Error("an empty recorded identity proved a process")
	}
	var nilProber *Prober
	if _, err := nilProber.Prove(41, broker("a")); err == nil {
		t.Error("a nil Prober proved a process")
	}
	if _, err := (&Prober{}).Prove(41, broker("a")); err == nil {
		t.Error("a Prober with nil seams proved a process")
	}
}

func TestSignalReadsTheIdentityAgainAndRefusesAChange(t *testing.T) {
	want := broker("darwin:100.5")
	k := newKernel(map[int]Identity{41: want})
	p := prober(t, k)
	c, err := p.Prove(41, want)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(c, syscall.SIGHUP, false); err != nil {
		t.Fatalf("Signal on a matching identity: %v", err)
	}
	if got := k.signals(); len(got) != 1 || got[0] != (sent{41, syscall.SIGHUP}) {
		t.Fatalf("signals = %v, want one SIGHUP to 41", got)
	}
	k.set(41, broker("darwin:200.1")) // the pid was reaped and handed to another process
	if err := p.Signal(c, syscall.SIGTERM, true); err == nil {
		t.Error("Signal succeeded after the identity changed")
	}
	delete(k.ids, 41)
	if err := p.Signal(c, syscall.SIGKILL, false); err == nil {
		t.Error("Signal succeeded after the process was gone")
	}
	if got := k.signals(); len(got) != 1 {
		t.Errorf("signals after the change = %v, want only the first SIGHUP", got)
	}
}

func TestSignalGroupOnlyWhenTheProvenProcessLeadsIt(t *testing.T) {
	for name, tc := range map[string]struct {
		pgid  int
		group bool
		want  sent
	}{
		"group, leader":      {pgid: 41, group: true, want: sent{-41, syscall.SIGTERM}},
		"group, not leader":  {pgid: 7, group: true, want: sent{41, syscall.SIGTERM}},
		"no group, leader":   {pgid: 41, group: false, want: sent{41, syscall.SIGTERM}},
		"no group, follower": {pgid: 7, group: false, want: sent{41, syscall.SIGTERM}},
	} {
		id := broker("a")
		id.Pgid = tc.pgid
		k := newKernel(map[int]Identity{41: id})
		p := prober(t, k)
		c, err := p.Prove(41, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Signal(c, syscall.SIGTERM, tc.group); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := k.signals(); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: signals = %v, want %v", name, got, tc.want)
		}
	}
}

func TestSignalRefusesWhatItMustNeverSend(t *testing.T) {
	id := broker("a")
	k := newKernel(map[int]Identity{0: id, 1: id, 41: id})
	p := prober(t, k)
	other := prober(t, newKernel(map[int]Identity{41: id}))
	good, err := p.Prove(41, id)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := other.Prove(41, id)
	if err != nil {
		t.Fatal(err)
	}
	var nilProber *Prober
	for name, run := range map[string]func() error{
		"the zero Child":              func() error { return p.Signal(Child{}, syscall.SIGKILL, false) },
		"the zero Child, group":       func() error { return p.Signal(Child{}, syscall.SIGKILL, true) },
		"pid 1":                       func() error { return p.Signal(Child{pid: 1, id: id, by: p}, syscall.SIGHUP, false) },
		"pid 1, group":                func() error { return p.Signal(Child{pid: 1, id: id, by: p}, syscall.SIGKILL, true) },
		"pid 0 with a Prober":         func() error { return p.Signal(Child{pid: 0, id: id, by: p}, syscall.SIGKILL, false) },
		"a negative pid":              func() error { return p.Signal(Child{pid: -1, id: id, by: p}, syscall.SIGKILL, true) },
		"a Child from another Prober": func() error { return p.Signal(foreign, syscall.SIGTERM, false) },
		"a nil Prober":                func() error { return nilProber.Signal(good, syscall.SIGTERM, false) },
		"a Prober with nil seams":     func() error { return (&Prober{}).Signal(good, syscall.SIGTERM, false) },
	} {
		if err := run(); err == nil {
			t.Errorf("%s: Signal returned nil", name)
		}
	}
	if got := k.signals(); len(got) != 0 {
		t.Errorf("a refused Signal still sent %v", got)
	}
}

func TestAdoptLegacyNeedsANatsServerRunningThisConf(t *testing.T) {
	const conf = "/run/nats.conf"
	for name, tc := range map[string]struct {
		live Identity
		ok   bool
	}{
		"nats-server with -c conf":     {live: broker("a"), ok: true},
		"another exe":                  {live: Identity{Start: "a", Exe: "/bin/sleep", Argv: []string{"sleep", "-c", conf}}},
		"nats-server, another conf":    {live: Identity{Start: "a", Exe: "/x/nats-server", Argv: []string{"nats-server", "-c", "/other.conf"}}},
		"nats-server, no -c":           {live: Identity{Start: "a", Exe: "/x/nats-server", Argv: []string{"nats-server", conf}}},
		"-c is the last argument":      {live: Identity{Start: "a", Exe: "/x/nats-server", Argv: []string{"nats-server", "-c"}}},
		"exe name only ends with it":   {live: Identity{Start: "a", Exe: "/x/not-nats-server", Argv: []string{"x", "-c", conf}}},
		"the conf is only a substring": {live: Identity{Start: "a", Exe: "/x/nats-server", Argv: []string{"nats-server", "-c", conf + ".bak"}}},
	} {
		k := newKernel(map[int]Identity{41: tc.live})
		c, err := prober(t, k).AdoptLegacy(41, conf)
		if tc.ok != (err == nil) {
			t.Errorf("%s: AdoptLegacy error = %v, want ok=%v", name, err, tc.ok)
			continue
		}
		if tc.ok && (c.Pid() != 41 || c.Identity().Start != "a") {
			t.Errorf("%s: Child = %+v", name, c)
		}
		if got := k.signals(); len(got) != 0 {
			t.Errorf("%s: adopting signalled %v", name, got)
		}
	}
	if _, err := prober(t, newKernel(nil)).AdoptLegacy(1, conf); err == nil {
		t.Error("AdoptLegacy accepted pid 1")
	}
}

func TestSameFollowsTheIdentity(t *testing.T) {
	id := broker("a")
	k := newKernel(map[int]Identity{41: id})
	p := prober(t, k)
	c, err := p.Prove(41, id)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Same(c) {
		t.Error("Same is false for the live process that was proven")
	}
	k.set(41, broker("b"))
	if p.Same(c) {
		t.Error("Same is true after the pid was handed to another process")
	}
	delete(k.ids, 41)
	if p.Same(c) {
		t.Error("Same is true for a process that is gone")
	}
	if p.Same(Child{}) {
		t.Error("Same is true for the zero Child")
	}
}

// The default reader is measured against the kernel, not a fake: the
// reader's output for a child this test started is stable across two reads,
// differs for a second child, and names the child as its own group leader.
// It runs on darwin locally and on ubuntu in CI (design section 7, item 8).
func TestReadIdentityOfARealChild(t *testing.T) {
	start := func() *exec.Cmd {
		cmd := exec.Command("sleep", "30")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Skipf("no sleep: %v", err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd
	}
	a, b := start(), start()
	first, err := readIdentity(a.Process.Pid)
	if err != nil {
		t.Fatalf("readIdentity(own child): %v", err)
	}
	again, err := readIdentity(a.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if first.Start != again.Start || first.Start == "" {
		t.Errorf("start time is not stable: %q then %q", first.Start, again.Start)
	}
	if filepath.Base(first.Exe) != "sleep" {
		t.Errorf("exe = %q, want a sleep binary", first.Exe)
	}
	if !slices.Equal(first.Argv, again.Argv) || len(first.Argv) != 2 || first.Argv[1] != "30" {
		t.Errorf("argv = %q then %q, want [.. 30]", first.Argv, again.Argv)
	}
	if first.Pgid != a.Process.Pid {
		t.Errorf("pgid = %d, want %d (Setpgid child leads its own group)", first.Pgid, a.Process.Pid)
	}
	other, err := readIdentity(b.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if other.Start == first.Start && a.Process.Pid != b.Process.Pid {
		// Two children can start within one tick on linux; the pid in the
		// comparison keeps this from being a false alarm there.
		t.Logf("two children read the same start %q (one clock tick)", first.Start)
	}
	// A real Prover accepts the real child and refuses a doctored record.
	p := New()
	if _, err := p.Prove(a.Process.Pid, first); err != nil {
		t.Errorf("Prove(own child, its own identity): %v", err)
	}
	doctored := first
	doctored.Start += "x"
	if _, err := p.Prove(a.Process.Pid, doctored); err == nil {
		t.Error("Prove accepted a doctored start time")
	}
	// A pid that does not exist reads as an error, not an empty identity.
	_ = a.Process.Kill()
	_ = a.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := readIdentity(a.Process.Pid); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("readIdentity still succeeds for a reaped child")
}
