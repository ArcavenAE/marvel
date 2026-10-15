package broker

import (
	"context"
	"errors"
	"sync"
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

func issueAsync(b *Broker, key, token string) <-chan issueResult {
	out := make(chan issueResult, 1)
	go func() {
		tok, err := b.Issue(context.Background(), key, token)
		out <- issueResult{tok, err}
	}()
	return out
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

	res := issueAsync(b, "ws/t/r-g1-0", "hb")
	waitFor(t, g.entered, "the driver to start issuing")
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	close(g.release)

	r := <-res
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

	first := issueAsync(b, "ws/t/r-g1-0", "hb")
	waitFor(t, g.entered, "the first ask to be in the driver")
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}

	later, err := b.Issue(context.Background(), "ws/t/r-g1-0", "hb")
	if err != nil {
		t.Fatalf("Issue that began after the revoke: %v", err)
	}
	close(g.release)
	<-first

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

// failingRevokeDriver fails Revoke while fail is set.
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
		return errors.New("idp down")
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

	res := issueAsync(b, "ws/t/r-g1-0", "hb")
	waitFor(t, g.entered, "the driver to start issuing")
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	f.setFail(true)
	close(g.release)
	if r := <-res; !errors.Is(r.err, ErrUnauthorized) {
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
