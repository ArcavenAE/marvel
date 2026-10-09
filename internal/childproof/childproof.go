// Package childproof holds the one way to signal a managed child: through a
// Child whose identity was read from the kernel and matched. A pid on its own
// names whichever process holds it now, so a recorded pid is never signalled;
// a Child is (pid, start time, executable, arguments) read and compared, and
// Signal reads it again before every signal (docs/design/bus-pidfile-identity.md
// section 4.5, requirement MS-9).
package childproof

import (
	"errors"
	"syscall"
)

// errUnbuilt marks the red stage of the build: nothing here works yet.
var errUnbuilt = errors.New("childproof: not built")

// errNoSeams is what a Prober without both seams answers.
var errNoSeams = errors.New("childproof: no prober seams")

// MismatchError says why a pid is not the process that was recorded. Reason
// is one of dead, start, exe or argv.
type MismatchError struct {
	Pid    int
	Reason string
}

func (e *MismatchError) Error() string { return "childproof: unproven" }

// Identity is what the kernel says about a process now.
type Identity struct {
	Start string // platform-tagged, compared as a string
	Exe   string
	Argv  []string
	Pgid  int
}

// Child is a process whose identity was read and matched.
type Child struct {
	pid int
	id  Identity
	by  *Prober
}

// Prober holds the two seams.
type Prober struct {
	read func(pid int) (Identity, error)
	kill func(pid int, sig syscall.Signal) error
}

// New returns the production Prober.
func New() *Prober { return &Prober{read: readIdentity, kill: syscall.Kill} }

// NewForTest returns a Prober with the given seams, in a test binary only.
func NewForTest(read func(int) (Identity, error), kill func(int, syscall.Signal) error) (*Prober, error) {
	return nil, errUnbuilt
}

// Prove reads pid's live identity and returns a Child when it equals want.
func (p *Prober) Prove(pid int, want Identity) (Child, error) { return Child{}, errUnbuilt }

// Identify reads pid's identity now, for a caller that has just started pid
// itself and holds its process handle.
func (p *Prober) Identify(pid int) (Identity, error) { return Identity{}, errUnbuilt }

// AdoptLegacy is the one constructor without a recorded identity (V11 option a).
func (p *Prober) AdoptLegacy(pid int, conf string) (Child, error) { return Child{}, errUnbuilt }

// Same reports whether c's process is still the one that was proven.
func (p *Prober) Same(c Child) bool { return false }

// Signal sends sig to c after reading its identity again.
func (p *Prober) Signal(c Child, sig syscall.Signal, group bool) error {
	if p == nil || p.read == nil || p.kill == nil {
		return errNoSeams
	}
	return errUnbuilt
}

// Identity returns what the proof read.
func (c Child) Identity() Identity { return c.id }

// Pid returns the proven pid, for logs and status.
func (c Child) Pid() int { return c.pid }

func readIdentity(pid int) (Identity, error) { return Identity{}, errUnbuilt }
