package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gateDriver holds the first Issue until released, so a test can end the seat
// while the driver is mid-issue. Later asks go straight through.
type gateDriver struct {
	Driver
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func newGateDriver(d Driver) *gateDriver {
	return &gateDriver{Driver: d, entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *gateDriver) Issue(ctx context.Context, r Request) (Token, error) {
	gated := false
	g.once.Do(func() { gated = true })
	if gated {
		close(g.entered)
		<-g.release
	}
	return g.Driver.Issue(ctx, r)
}

type issueResult struct {
	tok Token
	err error
}

// issueAsync asks for a token for the one seat these tests use.
func issueAsync(b *Broker) <-chan issueResult {
	out := make(chan issueResult, 1)
	go func() {
		tok, err := b.Issue(context.Background(), "ws/t/r-g1-0", "hb")
		out <- issueResult{tok, err}
	}()
	return out
}

// await returns an ask's result, failing the test if it never arrives, so a
// regression that strands an ask cannot hang the run.
func await(t *testing.T, ch <-chan issueResult) issueResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for an ask to finish")
		return issueResult{}
	}
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// A seat that ends while the driver is issuing must not keep the token: the
// revoke ran before the token was recorded, so the broker has to notice and
// revoke it itself.
func TestIssueRevokesATokenWhoseSeatEndedWhileTheDriverWasIssuing(t *testing.T) {
	t.Parallel()

	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	g := newGateDriver(m)
	b := New(g, fakeAuth{session: "ws/t/r-g1-0", token: "hb", id: Identity{Session: "ws/t/r-g1-0", Team: "t", Role: "r"}})

	res := issueAsync(b)
	waitFor(t, g.entered, "the driver to start issuing")
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	close(g.release)

	r := await(t, res)
	if !errors.Is(r.err, ErrUnauthorized) {
		t.Errorf("Issue after the seat ended = %v, want ErrUnauthorized", r.err)
	}
	if live := m.Live(); len(live) != 0 {
		t.Errorf("live tokens = %v, want none: a token was issued to a seat that had ended", live)
	}
}

// The end of one ask's window must not poison later asks: an Issue that began
// after the revoke is for whatever session now holds the key, and succeeds.
func TestIssueThatBeganAfterTheRevokeIsNotRefused(t *testing.T) {
	t.Parallel()

	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	g := newGateDriver(m)
	b := New(g, fakeAuth{session: "ws/t/r-g1-0", token: "hb", id: Identity{Session: "ws/t/r-g1-0", Team: "t", Role: "r"}})

	first := issueAsync(b)
	waitFor(t, g.entered, "the first ask to be in the driver")
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}

	later, err := b.Issue(context.Background(), "ws/t/r-g1-0", "hb")
	if err != nil {
		t.Fatalf("Issue that began after the revoke: %v", err)
	}
	close(g.release)
	await(t, first)

	if live := m.Live(); len(live) != 1 || live[0] != later.ID {
		t.Errorf("live tokens = %v, want only the later ask's %s", live, later.ID)
	}
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if live := m.Live(); len(live) != 0 {
		t.Errorf("live tokens after the second revoke = %v, want none", live)
	}
}

// errIdpRefused is the cause a failing driver wraps, so a test can tell the
// original error survives whatever the broker does to its text.
var errIdpRefused = errors.New("idp refused the revoke")

// failingRevokeDriver fails Revoke while fail is set, with an error that
// echoes the bearer value as an identity provider's error body can.
type failingRevokeDriver struct {
	Driver
	mu   sync.Mutex
	fail bool
}

func (f *failingRevokeDriver) setFail(v bool) {
	f.mu.Lock()
	f.fail = v
	f.mu.Unlock()
}

func (f *failingRevokeDriver) Revoke(ctx context.Context, t Token) error {
	f.mu.Lock()
	fail := f.fail
	f.mu.Unlock()
	if fail {
		return fmt.Errorf("%w: bearer %s", errIdpRefused, t.Value)
	}
	return f.Driver.Revoke(ctx, t)
}

// A token that could not be revoked when its ask was refused must stay on the
// broker's books, so that the next revoke for the seat retries it.
func TestIssueKeepsATokenItCouldNotRevokeForTheNextRevoke(t *testing.T) {
	t.Parallel()

	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	f := &failingRevokeDriver{Driver: m}
	g := newGateDriver(f)
	b := New(g, fakeAuth{session: "ws/t/r-g1-0", token: "hb", id: Identity{Session: "ws/t/r-g1-0", Team: "t", Role: "r"}})

	res := issueAsync(b)
	waitFor(t, g.entered, "the driver to start issuing")
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	f.setFail(true)
	close(g.release)
	if r := await(t, res); !errors.Is(r.err, ErrUnauthorized) {
		t.Fatalf("Issue after the seat ended = %v, want ErrUnauthorized", r.err)
	}

	f.setFail(false)
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("second RevokeSession: %v", err)
	}
	if live := m.Live(); len(live) != 0 {
		t.Errorf("live tokens after the second revoke = %v, want none: the failed self-revoke was forgotten", live)
	}
}

// A driver's revoke error can echo the request, bearer value included. The
// broker's errors are logged by callers, so none may carry the value, and the
// original must stay reachable with errors.Is.
func TestRevokeErrorsCarryNoTokenValue(t *testing.T) {
	t.Parallel()

	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	f := &failingRevokeDriver{Driver: m}
	b := New(f, fakeAuth{session: "ws/t/r-g1-0", token: "hb", id: Identity{Session: "ws/t/r-g1-0", Team: "t", Role: "r"}})
	tok, err := b.Issue(context.Background(), "ws/t/r-g1-0", "hb")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	f.setFail(true)

	err = b.RevokeSession(context.Background(), "ws/t/r-g1-0")
	if err == nil {
		t.Fatal("RevokeSession with a failing driver returned nil")
	}
	if strings.Contains(err.Error(), tok.Value) {
		t.Errorf("RevokeSession error carries the token value: %q", err)
	}
	if !errors.Is(err, errIdpRefused) {
		t.Errorf("RevokeSession error = %v, want the driver's error still reachable with errors.Is", err)
	}
}

func TestSelfRevokeErrorCarriesNoTokenValue(t *testing.T) {
	t.Parallel()

	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	f := &failingRevokeDriver{Driver: m}
	g := newGateDriver(f)
	b := New(g, fakeAuth{session: "ws/t/r-g1-0", token: "hb", id: Identity{Session: "ws/t/r-g1-0", Team: "t", Role: "r"}})

	res := issueAsync(b)
	waitFor(t, g.entered, "the driver to start issuing")
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	f.setFail(true)
	close(g.release)

	r := await(t, res)
	if !errors.Is(r.err, ErrUnauthorized) || !errors.Is(r.err, errIdpRefused) {
		t.Fatalf("Issue error = %v, want ErrUnauthorized with the driver's error reachable", r.err)
	}
	for _, id := range m.Live() {
		if strings.Contains(r.err.Error(), "mock."+id) {
			t.Errorf("Issue error carries the token value: %q", r.err)
		}
	}
}

// A revoke that fails must not make the broker forget the token: the next
// revoke for the seat retries it.
func TestRevokeSessionKeepsATokenWhoseRevokeFailed(t *testing.T) {
	t.Parallel()

	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	f := &failingRevokeDriver{Driver: m}
	b := New(f, fakeAuth{session: "ws/t/r-g1-0", token: "hb", id: Identity{Session: "ws/t/r-g1-0", Team: "t", Role: "r"}})
	if _, err := b.Issue(context.Background(), "ws/t/r-g1-0", "hb"); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	f.setFail(true)
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err == nil {
		t.Fatal("RevokeSession with a failing driver returned nil")
	}
	f.setFail(false)
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("second RevokeSession: %v", err)
	}
	if live := m.Live(); len(live) != 0 {
		t.Errorf("live tokens after the retry = %v, want none: the failed revoke was forgotten", live)
	}
}

// Once a seat's row is gone nothing reports that key again, so a token whose
// revoke failed needs a pass that retries keys with no session.
func TestRetryRevokesRetriesTheKeysOfSeatsThatAreGone(t *testing.T) {
	t.Parallel()

	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	f := &failingRevokeDriver{Driver: m}
	b := New(f, multiAuth{})
	for _, key := range []string{"ws/t/gone-g1-0", "ws/t/here-g1-0"} {
		if _, err := b.Issue(context.Background(), key, "t"+key); err != nil {
			t.Fatalf("Issue %s: %v", key, err)
		}
	}
	f.setFail(true)
	if err := b.RevokeSession(context.Background(), "ws/t/gone-g1-0"); err == nil {
		t.Fatal("RevokeSession with a failing driver returned nil")
	}
	f.setFail(false)

	exists := func(key string) bool { return key == "ws/t/here-g1-0" }
	if err := b.RetryRevokes(context.Background(), exists); err != nil {
		t.Fatalf("RetryRevokes: %v", err)
	}
	if live := m.Live(); len(live) != 1 {
		t.Fatalf("live tokens after the pass = %v, want only the seat that still exists", live)
	}
	if err := b.RetryRevokes(context.Background(), exists); err != nil {
		t.Fatalf("second RetryRevokes: %v", err)
	}
	if live := m.Live(); len(live) != 1 {
		t.Errorf("live tokens after a second pass = %v, want the existing seat's token left alone", live)
	}
}

// A pass that fails again keeps the token for the next pass.
func TestRetryRevokesKeepsATokenItCannotRevokeYet(t *testing.T) {
	t.Parallel()

	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	f := &failingRevokeDriver{Driver: m}
	b := New(f, multiAuth{})
	if _, err := b.Issue(context.Background(), "ws/t/gone-g1-0", "tws/t/gone-g1-0"); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	f.setFail(true)
	none := func(string) bool { return false }
	if err := b.RetryRevokes(context.Background(), none); err == nil {
		t.Fatal("RetryRevokes with a failing driver returned nil")
	}
	f.setFail(false)
	if err := b.RetryRevokes(context.Background(), none); err != nil {
		t.Fatalf("RetryRevokes after the driver recovered: %v", err)
	}
	if live := m.Live(); len(live) != 0 {
		t.Errorf("live tokens = %v, want none", live)
	}
}

// armedGate holds an Issue only after arm is called, so a test can let one
// seat's tokens through and stop the next seat's ask inside the driver.
type armedGate struct {
	Driver
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func newArmedGate(d Driver) *armedGate {
	return &armedGate{Driver: d, entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *armedGate) Issue(ctx context.Context, r Request) (Token, error) {
	if g.armed.CompareAndSwap(true, false) {
		close(g.entered)
		<-g.release
	}
	return g.Driver.Issue(ctx, r)
}

// A retry pass decides a key's seat is gone and then revokes. A seat that
// takes the same name in between holds tokens the pass must not touch.
func TestRetryRevokesLeavesTheTokenOfASeatThatTookTheNameMeanwhile(t *testing.T) {
	t.Parallel()

	const key = "ws/t/r-g1-0"
	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	f := &failingRevokeDriver{Driver: m}
	b := New(f, multiAuth{})
	old, err := b.Issue(context.Background(), key, "t"+key)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	f.setFail(true)
	if err := b.RevokeSession(context.Background(), key); err == nil {
		t.Fatal("RevokeSession with a failing driver returned nil")
	}
	f.setFail(false)

	var fresh Token
	exists := func(string) bool {
		// A new seat takes the name and is issued a token before this answer lands.
		tok, ierr := b.Issue(context.Background(), key, "t"+key)
		if ierr != nil {
			t.Errorf("the new seat's Issue: %v", ierr)
		}
		fresh = tok
		return false
	}
	if err := b.RetryRevokes(context.Background(), exists); err != nil {
		t.Fatalf("RetryRevokes: %v", err)
	}

	live := m.Live()
	if len(live) != 1 || live[0] != fresh.ID {
		t.Errorf("live tokens = %v, want only the new seat's %s (the old seat's %s revoked)", live, fresh.ID, old.ID)
	}
}

// The same race with the new seat's ask still inside the driver: the pass
// must not count as a revoke of that seat, or its ask is refused.
func TestRetryRevokesDoesNotRefuseAnAskInFlightForTheSameName(t *testing.T) {
	t.Parallel()

	const key = "ws/t/r-g1-0"
	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	f := &failingRevokeDriver{Driver: m}
	g := newArmedGate(f)
	b := New(g, multiAuth{})
	if _, err := b.Issue(context.Background(), key, "t"+key); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	f.setFail(true)
	if err := b.RevokeSession(context.Background(), key); err == nil {
		t.Fatal("RevokeSession with a failing driver returned nil")
	}
	f.setFail(false)

	res := make(chan issueResult, 1)
	exists := func(string) bool {
		g.armed.Store(true)
		go func() {
			tok, err := b.Issue(context.Background(), key, "t"+key)
			res <- issueResult{tok, err}
		}()
		waitFor(t, g.entered, "the new seat's ask to be inside the driver")
		return false
	}
	if err := b.RetryRevokes(context.Background(), exists); err != nil {
		t.Fatalf("RetryRevokes: %v", err)
	}
	close(g.release)

	r := await(t, res)
	if r.err != nil {
		t.Fatalf("the new seat's ask during a retry pass = %v, want it issued", r.err)
	}
	if live := m.Live(); len(live) != 1 || live[0] != r.tok.ID {
		t.Errorf("live tokens = %v, want only the new seat's %s", live, r.tok.ID)
	}
}

// aliasAuth admits a token for whatever key the caller typed and answers with
// the session's canonical identity, as a lookup that is not exact would.
type aliasAuth struct{ canonical string }

func (a aliasAuth) Authenticate(_, token string) (Identity, error) {
	if token != "hb" {
		return Identity{}, ErrUnauthorized
	}
	return Identity{Session: a.canonical, Team: "t", Role: "r"}, nil
}

// The token record hangs off the identity the broker authenticated, so the
// seat's end, which names the canonical key, revokes it.
func TestIssueRecordsTheTokenUnderTheAuthenticatedSession(t *testing.T) {
	t.Parallel()

	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	b := New(m, aliasAuth{canonical: "ws/t/r-g1-0"})
	if _, err := b.Issue(context.Background(), "WS/T/R-G1-0", "hb"); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if live := m.Live(); len(live) != 0 {
		t.Errorf("live tokens after the seat ended = %v, want none: the token was filed under the typed key", live)
	}
}
