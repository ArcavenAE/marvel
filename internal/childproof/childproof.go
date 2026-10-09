// Package childproof holds the one way to signal a managed child: through a
// Child whose identity was read from the kernel and matched. A pid on its own
// names whichever process holds it now, so a recorded pid is never signalled;
// a Child is (pid, start time, executable, arguments) read and compared, and
// Signal reads it again before every signal (docs/design/bus-pidfile-identity.md
// section 4.5, requirement MS-9).
//
// The types keep their fields unexported so that code outside this package can
// build a Child only through Prove and AdoptLegacy, and a Prober only through
// New and NewForTest. The platform readers sit in build-tagged files beside
// this one.
package childproof

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// errNoSeams is what a Prober without both seams answers. A wiring mistake
// leaves the daemon up with the child unproven, never panicking.
var errNoSeams = errors.New("childproof: prober has no read or kill seam")

// ErrUnsignalable is returned by Signal for a Child it will not signal: the
// zero Child, a pid at or below 1, or one built by another Prober.
var ErrUnsignalable = errors.New("childproof: not a child this prober proved")

// MismatchError says why a pid is not the process that was recorded. Reason
// is one of dead, start, exe or argv.
type MismatchError struct {
	Pid    int
	Reason string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("childproof: pid %d is not the recorded process (%s)", e.Pid, e.Reason)
}

// Identity is what the kernel says about a process now.
type Identity struct {
	// Start is platform-tagged and compared as a string: darwin:<sec>.<usec>
	// from p_starttime, linux:<boot_id>:<ticks> from stat field 22. A state
	// directory copied between systems never matches.
	Start string
	// Exe is the executable the kernel reports for the running process.
	Exe  string
	Argv []string
	// Pgid is the process group, read live and never recorded.
	Pgid int
}

// Child is a process whose identity was read and matched. Its fields are
// unexported, so only Prove and AdoptLegacy build one. It records the Prober
// that built it. The zero value has pid 0 and no Prober, and Signal refuses it.
type Child struct {
	pid int
	id  Identity
	by  *Prober
}

// Identity returns what the proof read, so the caller can write the sidecar.
func (c Child) Identity() Identity { return c.id }

// Pid returns the proven pid, for logs and status.
func (c Child) Pid() int { return c.pid }

// Prober holds the two seams. Both are unexported, so code outside this
// package cannot build a Prober whose read lies or whose kill is real by
// accident; it gets one from New or NewForTest.
type Prober struct {
	read func(pid int) (Identity, error)
	kill func(pid int, sig syscall.Signal) error
}

// New returns the production Prober: the platform reader and syscall.Kill.
func New() *Prober { return &Prober{read: readIdentity, kill: syscall.Kill} }

// NewForTest returns a Prober with the given seams. It refuses unless the
// binary is a test binary, and refuses a nil seam, so a test cannot get the
// real kill by leaving one unset. testing.Testing() is also true for non-test
// code linked into a test binary, so a source test fails on any reference to
// NewForTest outside a _test.go file.
func NewForTest(read func(int) (Identity, error), kill func(int, syscall.Signal) error) (*Prober, error) {
	if !testing.Testing() {
		return nil, errors.New("childproof: NewForTest outside a test binary")
	}
	if read == nil || kill == nil {
		return nil, errNoSeams
	}
	return &Prober{read: read, kill: kill}, nil
}

func (p *Prober) wired() bool { return p != nil && p.read != nil && p.kill != nil }

// Identify reads pid's identity now, for a caller that has just started pid
// itself and holds its process handle. It proves nothing about a pid read
// from a file.
func (p *Prober) Identify(pid int) (Identity, error) {
	if !p.wired() {
		return Identity{}, errNoSeams
	}
	if pid <= 1 {
		return Identity{}, fmt.Errorf("childproof: pid %d names no single process", pid)
	}
	return p.read(pid)
}

// Prove reads pid's live identity and returns a Child when it equals want:
// the same start time, executable and arguments (design section 4.1, rules 2
// to 5). A pid of 1 or below, an empty want and a Prober without seams are
// refused.
func (p *Prober) Prove(pid int, want Identity) (Child, error) {
	if !p.wired() {
		return Child{}, errNoSeams
	}
	if pid <= 1 {
		return Child{}, fmt.Errorf("childproof: pid %d names no single process", pid)
	}
	if want.Start == "" {
		return Child{}, errors.New("childproof: no recorded start time to prove against")
	}
	live, err := p.read(pid)
	if err != nil {
		return Child{}, &MismatchError{Pid: pid, Reason: "dead"}
	}
	switch {
	case live.Start != want.Start:
		return Child{}, &MismatchError{Pid: pid, Reason: "start"}
	case live.Exe != want.Exe:
		return Child{}, &MismatchError{Pid: pid, Reason: "exe"}
	case !slices.Equal(live.Argv, want.Argv):
		return Child{}, &MismatchError{Pid: pid, Reason: "argv"}
	}
	return Child{pid: pid, id: live, by: p}, nil
}

// AdoptLegacy is the one constructor without a recorded identity: V11 option
// (a) only. It returns a Child when the live executable's base name is
// nats-server and its arguments carry -c followed by conf. It is deleted if
// V11 is ruled (b).
func (p *Prober) AdoptLegacy(pid int, conf string) (Child, error) {
	if !p.wired() {
		return Child{}, errNoSeams
	}
	if pid <= 1 {
		return Child{}, fmt.Errorf("childproof: pid %d names no single process", pid)
	}
	live, err := p.read(pid)
	if err != nil {
		return Child{}, &MismatchError{Pid: pid, Reason: "dead"}
	}
	if live.Start == "" || filepath.Base(live.Exe) != "nats-server" {
		return Child{}, &MismatchError{Pid: pid, Reason: "exe"}
	}
	if !carriesConf(live.Argv, conf) {
		return Child{}, &MismatchError{Pid: pid, Reason: "argv"}
	}
	return Child{pid: pid, id: live, by: p}, nil
}

// carriesConf reports whether argv holds -c immediately followed by conf.
func carriesConf(argv []string, conf string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-c" && argv[i+1] == conf {
			return true
		}
	}
	return false
}

// Same reports whether c's process is still the one that was proven: the same
// start time, executable and arguments. It is the "is it still alive" check
// that cannot be fooled by a reused pid.
func (p *Prober) Same(c Child) bool {
	if !p.wired() || c.by != p || c.pid <= 1 {
		return false
	}
	live, err := p.read(c.pid)
	return err == nil && sameIdentity(live, c.id)
}

func sameIdentity(a, b Identity) bool {
	return a.Start == b.Start && a.Exe == b.Exe && slices.Equal(a.Argv, b.Argv)
}

// Signal sends sig to c after reading its identity again, so a pid reused since
// the proof is never signalled. It refuses a nil Prober, a nil seam, a Child
// built by another Prober, the zero Child, and pid 1 or below, for every
// signal: kill(0, sig) signals the caller's own group and kill(-1, sig) every
// process. With group set it signals -pid, but only when the live process
// leads its own group (pgid == pid); a proven process that does not is signalled
// alone.
func (p *Prober) Signal(c Child, sig syscall.Signal, group bool) error {
	if !p.wired() {
		return errNoSeams
	}
	if c.by != p || c.pid <= 1 {
		return ErrUnsignalable
	}
	live, err := p.read(c.pid)
	if err != nil {
		return &MismatchError{Pid: c.pid, Reason: "dead"}
	}
	if !sameIdentity(live, c.id) {
		return &MismatchError{Pid: c.pid, Reason: reasonOf(live, c.id)}
	}
	target := c.pid
	if group && live.Pgid == c.pid {
		target = -c.pid
	}
	if err := p.kill(target, sig); err != nil {
		return fmt.Errorf("childproof: signal %d to %d: %w", sig, target, err)
	}
	return nil
}

func reasonOf(live, want Identity) string {
	switch {
	case live.Start != want.Start:
		return "start"
	case live.Exe != want.Exe:
		return "exe"
	default:
		return "argv"
	}
}

// parseStat reads /proc/<pid>/stat: field 5 (the process group) and field 22
// (the start time in clock ticks since boot). The command name is field 2 and
// may hold spaces and parentheses, so fields are counted from the last ')'.
func parseStat(b []byte) (pgrp int, startTicks uint64, err error) {
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, 0, errors.New("childproof: stat has no command field")
	}
	f := strings.Fields(s[i+1:])
	// f[0] is field 3 (state); field n is f[n-3].
	if len(f) < 20 {
		return 0, 0, fmt.Errorf("childproof: stat has %d fields after the command, want 20 or more", len(f))
	}
	if _, err := fmt.Sscanf(f[2], "%d", &pgrp); err != nil {
		return 0, 0, fmt.Errorf("childproof: stat process group %q: %w", f[2], err)
	}
	if _, err := fmt.Sscanf(f[19], "%d", &startTicks); err != nil {
		return 0, 0, fmt.Errorf("childproof: stat start time %q: %w", f[19], err)
	}
	return pgrp, startTicks, nil
}
