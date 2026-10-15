package daemon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/arcavenae/marvel/internal/api"
)

// The credential handoff (aae-orc-4h9xp, marvel#339). A reexec replaces the
// daemon's process image; the transient credentials it holds live only in
// memory, so without a carry the successor starts with an empty store. The
// departing daemon writes them to a pipe, passes the descriptor number in one
// environment variable, and the successor reads the pipe first thing and closes
// it. Nothing is written to disk, to the environment, to a log or to an event:
// the environment holds a descriptor number and nothing else.

const (
	// handoffEnvVar names the descriptor the successor reads. Its value is a
	// number. It is never set with os.Setenv: it is added to the environment
	// handed to the exec and nowhere else.
	handoffEnvVar = "MARVEL_REEXEC_HANDOFF_FD"

	// ReexecHandoffFormat is the payload format this binary writes and reads.
	// `marvel version --capabilities --json` reports it, and the departing
	// daemon asks its target for it before it creates anything.
	ReexecHandoffFormat = 1

	// handoffCap bounds the payload well under the smallest pipe buffer (16 KiB
	// on macOS), so the write never blocks on a reader that does not exist yet.
	handoffCap = 12 * 1024

	// handoffReadWait bounds how long the successor waits on a pipe whose
	// writer end is somehow still open. The departing daemon closes it before
	// the exec, so a normal read ends at once.
	handoffReadWait = 2 * time.Second

	handoffProbeTimeout = 2 * time.Second
)

var handoffMagic = [4]byte{'M', 'V', 'H', 'F'}

// carryTable is the written decision for every credential kind: carried across
// a reexec (true) or dropped (false). A kind absent from it cannot travel or be
// dropped by accident, and a test fails until a new kind is decided here. The
// recorded dissent in the design party is that carrying is a custody act per
// audience; nats-nkey-seed is issuance inside the ADR-009 boundary, since the
// daemon could re-request it.
var carryTable = map[api.CredentialKind]bool{
	api.CredentialNATSNKeySeed: true,
}

// Outcomes of the departing side's attempt, as the reexec response reports
// them before the exec.
const (
	handoffNone     = "none"      // nothing to carry
	handoffCarried  = "carried"   // written to the pipe; the exec will pass it on
	handoffDeclined = "declined"  // the target cannot read the handoff
	handoffOverCap  = "over-cap"  // the payload would not fit
	handoffFailed   = "failed"    // a pipe or write error
	reasonDeclined  = "target cannot read the handoff"
	reasonOverCap   = "payload over cap"
	reasonFailed    = "handoff failed"
	reasonNotCarry  = "kind not in the carry table"
	causeUnreadText = "handoff unread"
)

// --- the wire format -----------------------------------------------------------
//
// magic "MVHF", the format byte, a big-endian uint16 count, then per credential
// the strings name, kind, audience, binding and issued_by (each a uint16 length
// then bytes), issued_at (int64 unix nanoseconds, big-endian) and the value (a
// uint16 length then bytes). Hand-framed so the value is raw bytes that can be
// zeroed, never a Go string or a base64 copy.

func putStr(b *bytes.Buffer, s []byte) error {
	if len(s) > 0xffff {
		return errors.New("a field is too long to carry")
	}
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], uint16(len(s)))
	b.Write(n[:])
	b.Write(s)
	return nil
}

// encodeHandoff frames creds. The caller zeroes what it passed in.
func encodeHandoff(creds []api.Credential) ([]byte, error) {
	var b bytes.Buffer
	b.Write(handoffMagic[:])
	b.WriteByte(ReexecHandoffFormat)
	if len(creds) > 0xffff {
		return nil, errors.New("too many credentials to carry")
	}
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], uint16(len(creds)))
	b.Write(n[:])
	for _, c := range creds {
		for _, s := range []string{c.Name, string(c.Kind), c.Audience, c.Binding, c.IssuedBy} {
			if err := putStr(&b, []byte(s)); err != nil {
				return nil, err
			}
		}
		var t [8]byte
		binary.BigEndian.PutUint64(t[:], uint64(c.IssuedAt.UnixNano()))
		b.Write(t[:])
		if err := putStr(&b, c.Value); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

// decodeHandoff parses a payload. Any short, garbled, oversized or
// wrong-version input returns an error and no credentials: never a partial one.
func decodeHandoff(buf []byte) ([]api.Credential, error) {
	if len(buf) > handoffCap {
		return nil, errors.New("handoff payload over cap")
	}
	if len(buf) < len(handoffMagic)+1+2 || !bytes.Equal(buf[:4], handoffMagic[:]) {
		return nil, errors.New("handoff payload has no header")
	}
	if buf[4] != ReexecHandoffFormat {
		return nil, fmt.Errorf("handoff payload format %d, this binary reads %d", buf[4], ReexecHandoffFormat)
	}
	count := int(binary.BigEndian.Uint16(buf[5:7]))
	rest := buf[7:]
	take := func(n int) ([]byte, bool) {
		if n > len(rest) {
			return nil, false
		}
		out := rest[:n]
		rest = rest[n:]
		return out, true
	}
	str := func() ([]byte, bool) {
		h, ok := take(2)
		if !ok {
			return nil, false
		}
		return take(int(binary.BigEndian.Uint16(h)))
	}
	var out []api.Credential
	fail := func() ([]api.Credential, error) {
		for i := range out {
			for j := range out[i].Value {
				out[i].Value[j] = 0
			}
		}
		return nil, errors.New("handoff payload is short or garbled")
	}
	for i := 0; i < count; i++ {
		var f [5][]byte
		for k := range f {
			s, ok := str()
			if !ok {
				return fail()
			}
			f[k] = s
		}
		t, ok := take(8)
		if !ok {
			return fail()
		}
		v, ok := str()
		if !ok {
			return fail()
		}
		c := api.Credential{
			Name: string(f[0]), Kind: api.CredentialKind(f[1]), Audience: string(f[2]),
			Binding: string(f[3]), IssuedBy: string(f[4]),
			IssuedAt: time.Unix(0, int64(binary.BigEndian.Uint64(t))).UTC(),
			Value:    append([]byte(nil), v...),
		}
		if !api.ValidCredentialKind(c.Kind) {
			return fail()
		}
		out = append(out, c)
	}
	if len(rest) != 0 {
		return fail()
	}
	return out, nil
}

// --- the guard -----------------------------------------------------------------

// probeReexecTarget asks the binary about to be exec'd whether it can read the
// handoff, before the pipe exists. The answer is `marvel version --capabilities
// --json` with reexec_handoff equal to this binary's format. Anything else is a
// decline: an older binary that rejects the flag, a timeout, an error, no
// answer. The probe runs with an empty environment and no inherited
// descriptors, and never dials a daemon socket.
func probeReexecTarget(exe string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "version", "--capabilities", "--json")
	cmd.Env = []string{}
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &out, left: 4096}
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("probe %s: %w", exe, err)
	}
	var got map[string]int
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		return fmt.Errorf("probe %s: not a capability answer: %w", exe, err)
	}
	if got["reexec_handoff"] != ReexecHandoffFormat {
		return fmt.Errorf("probe %s: reexec_handoff is %d, want %d", exe, got["reexec_handoff"], ReexecHandoffFormat)
	}
	return nil
}

type limitedWriter struct {
	w    *bytes.Buffer
	left int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n > l.left {
		p = p[:l.left]
	}
	l.left -= len(p)
	l.w.Write(p)
	return n, nil
}

// --- the departing side ----------------------------------------------------------

// reexecHandoff is one prepared attempt: the outcome, how many credentials it
// covers, and the read end of the pipe when the outcome is carried.
type reexecHandoff struct {
	outcome string
	reason  string
	n       int // credentials the attempt is about
	rfd     int // read end, -1 when there is none
	wfd     int // write end, -1 once closed
	buf     []byte
}

// Outcome is what the reexec response reports: none, carried, declined,
// over-cap or failed.
func (h *reexecHandoff) Outcome() string { return h.outcome }

// wiped reports whether the payload buffer holds only zeros.
func (h *reexecHandoff) wiped() bool {
	for _, b := range h.buf {
		if b != 0 {
			return false
		}
	}
	return true
}

func (h *reexecHandoff) zero() {
	for i := range h.buf {
		h.buf[i] = 0
	}
}

func closeFD(fd *int) {
	if *fd >= 0 {
		_ = unix.Close(*fd)
		*fd = -1
	}
}

// abort closes both ends and zeroes the buffer. It is the exec-error path and
// every failure path, and is safe to call twice.
func (h *reexecHandoff) abort() {
	closeFD(&h.rfd)
	closeFD(&h.wfd)
	h.zero()
}

// arm clears FD_CLOEXEC on the read end, the last act before the exec, and
// returns the environment entry that names it.
func (h *reexecHandoff) arm() (string, error) {
	if h.rfd < 0 {
		return "", errors.New("no handoff descriptor")
	}
	if _, err := unix.FcntlInt(uintptr(h.rfd), unix.F_SETFD, 0); err != nil {
		return "", fmt.Errorf("clear close-on-exec: %w", err)
	}
	return handoffEnvVar + "=" + strconv.Itoa(h.rfd), nil
}

func (h *reexecHandoff) failWith(outcome, reason string) *reexecHandoff {
	h.abort()
	h.outcome, h.reason = outcome, reason
	return h
}

// prepareHandoff decides and builds the handoff for a reexec into exe: it
// snapshots the carried credentials, asks the target (before creating
// anything), writes the payload to a pipe and closes the write end. It never
// returns an error: every path that cannot carry says so in the outcome.
func (d *Daemon) prepareHandoff(exe string) *reexecHandoff {
	h := &reexecHandoff{outcome: handoffNone, rfd: -1, wfd: -1}
	metas := d.store.ListCredentials()
	h.n = len(metas)
	if h.n == 0 {
		return h
	}
	var carry []api.Credential
	for _, m := range metas {
		if !carryTable[m.Kind] {
			continue
		}
		v, err := d.store.RevealCredentialValue(m.Name)
		if err != nil {
			continue
		}
		m.Value = v
		carry = append(carry, m)
	}
	wipeCreds := func() {
		for i := range carry {
			for j := range carry[i].Value {
				carry[i].Value[j] = 0
			}
		}
	}
	defer wipeCreds()
	if len(carry) == 0 {
		h.reason = reasonNotCarry
		return h
	}
	probe := d.handoffProbe
	if probe == nil {
		probe = func(exe string) error { return probeReexecTarget(exe, handoffProbeTimeout) }
	}
	if err := probe(exe); err != nil {
		log.Printf("reexec handoff: %v", err)
		return h.failWith(handoffDeclined, reasonDeclined)
	}
	buf, err := encodeHandoff(carry)
	if err != nil || len(buf) > handoffCap {
		for i := range buf {
			buf[i] = 0
		}
		return h.failWith(handoffOverCap, reasonOverCap)
	}
	h.buf = buf
	// Both ends close on exec until arm clears the read end. The fork lock
	// keeps a concurrent fork from inheriting them in the gap before the flag.
	var fds [2]int
	syscall.ForkLock.RLock()
	perr := syscall.Pipe(fds[:])
	if perr == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if perr != nil {
		return h.failWith(handoffFailed, reasonFailed)
	}
	h.rfd, h.wfd = fds[0], fds[1]
	for off := 0; off < len(buf); {
		n, werr := unix.Write(h.wfd, buf[off:])
		if werr != nil && !errors.Is(werr, unix.EINTR) {
			return h.failWith(handoffFailed, reasonFailed)
		}
		if n > 0 {
			off += n
		}
	}
	closeFD(&h.wfd)
	h.zero()
	h.outcome = handoffCarried
	return h
}

// takeHandoff returns the handoff handleReexec prepared for the response, once.
func (d *Daemon) takeHandoff() *reexecHandoff {
	d.handoffMu.Lock()
	defer d.handoffMu.Unlock()
	h := d.pendingHandoff
	d.pendingHandoff = nil
	return h
}

func (d *Daemon) setPendingHandoff(h *reexecHandoff) {
	d.handoffMu.Lock()
	defer d.handoffMu.Unlock()
	if d.pendingHandoff != nil {
		d.pendingHandoff.abort()
	}
	d.pendingHandoff = h
}

// handoffCarriedCount is how many credentials the departing daemon is carrying,
// so shutdown does not report them dropped.
func (d *Daemon) handoffCarriedCount() int {
	d.handoffMu.Lock()
	defer d.handoffMu.Unlock()
	if d.lastHandoff != nil && d.lastHandoff.outcome == handoffCarried {
		return d.lastHandoff.n
	}
	return 0
}

// withoutHandoffVar drops any handoff variable from an environment, so a stale
// value from a previous hop never rides into the next exec.
func withoutHandoffVar(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, handoffEnvVar+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// execWithHandoff is the tail of ReexecTo: it adds the descriptor number to the
// environment when there is one to pass, logs what was carried or dropped, and
// calls the exec. An exec that returns an error closes and zeroes both ends.
func (d *Daemon) execWithHandoff(exe string, h *reexecHandoff) error {
	envv := withoutHandoffVar(os.Environ())
	switch h.outcome {
	case handoffCarried:
		entry, err := h.arm()
		if err != nil {
			log.Printf("reexec handoff: dropped %d, %s: %v", h.n, reasonFailed, err)
			h.abort()
		} else {
			envv = append(envv, entry)
			log.Printf("reexec handoff: carried %d transient credential(s)", h.n)
		}
	case handoffNone:
		if h.n > 0 {
			log.Printf("reexec handoff: dropped %d, %s", h.n, h.reason)
		}
	default:
		log.Printf("reexec handoff: dropped %d, %s", h.n, h.reason)
	}
	if err := d.reexec(exe, os.Args, envv); err != nil {
		h.abort()
		return err
	}
	return nil
}

// --- the receiving side ------------------------------------------------------------

type handoffState int

const (
	handoffAbsent handoffState = iota // no variable: a plain start, or a decline
	handoffRead                       // read and decoded
	handoffUnread                     // announced and not readable
)

type handoffResult struct {
	State handoffState
	Err   error
}

// receiveReexecHandoff reads the handoff a departing daemon left, if any. It
// runs first in New, before any other descriptor is opened and before any child
// is spawned: it unsets the variable at once (a child would inherit it), checks
// that the descriptor is a FIFO, reads it to EOF, closes it and decodes. It
// never closes a descriptor that is not a FIFO, since that one is not its pipe.
// A bad payload gives no credentials, never a partial one.
func receiveReexecHandoff() ([]api.Credential, handoffResult) {
	v, ok := os.LookupEnv(handoffEnvVar)
	if !ok {
		return nil, handoffResult{State: handoffAbsent}
	}
	_ = os.Unsetenv(handoffEnvVar)
	fd64, err := strconv.ParseInt(v, 10, 32)
	if err != nil || fd64 < 3 {
		return nil, handoffResult{State: handoffUnread, Err: errors.New("the handoff descriptor is not a usable number")}
	}
	fd := int(fd64)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, handoffResult{State: handoffUnread, Err: fmt.Errorf("the handoff descriptor is not open: %w", err)}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFIFO {
		return nil, handoffResult{State: handoffUnread, Err: errors.New("the handoff descriptor is not a pipe")}
	}
	buf, rerr := readPipe(fd, handoffCap+1, handoffReadWait)
	_ = unix.Close(fd)
	defer func() {
		for i := range buf {
			buf[i] = 0
		}
	}()
	if rerr != nil {
		return nil, handoffResult{State: handoffUnread, Err: rerr}
	}
	creds, derr := decodeHandoff(buf)
	if derr != nil {
		return nil, handoffResult{State: handoffUnread, Err: derr}
	}
	return creds, handoffResult{State: handoffRead}
}

// readPipe reads fd to EOF, at most max bytes, waiting no longer than wait in
// all.
func readPipe(fd, max int, wait time.Duration) ([]byte, error) {
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	buf := make([]byte, 0, 512)
	chunk := make([]byte, 512)
	for {
		n, err := unix.Read(fd, chunk)
		switch {
		case n > 0:
			buf = append(buf, chunk[:n]...)
			if len(buf) >= max {
				return buf, errors.New("handoff payload over cap")
			}
		case err == nil:
			return buf, nil // EOF
		case errors.Is(err, unix.EINTR):
		case errors.Is(err, unix.EAGAIN):
			left := time.Until(deadline)
			if left <= 0 {
				return buf, errors.New("handoff pipe never closed")
			}
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			_, _ = unix.Poll(fds, int(left/time.Millisecond)+1)
		default:
			return buf, err
		}
		for i := range chunk {
			chunk[i] = 0
		}
	}
}
