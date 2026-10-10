package broker

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// fakeAuth admits one session with one token.
type fakeAuth struct {
	session, token string
	id             Identity
}

func (f fakeAuth) Authenticate(key, token string) (Identity, error) {
	if key != f.session || token != f.token {
		return Identity{}, ErrUnauthorized
	}
	return f.id, nil
}

// recordingDriver records what it was asked and can fail a revoke.
type recordingDriver struct {
	mu       sync.Mutex
	issued   []Request
	revoked  []string
	failFor  string
	nextID   int
	lastReq  Request
	issueErr error
}

func (d *recordingDriver) Name() string { return "recording" }
func (d *recordingDriver) Issue(_ context.Context, r Request) (Token, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.issueErr != nil {
		return Token{}, d.issueErr
	}
	d.nextID++
	d.issued = append(d.issued, r)
	d.lastReq = r
	id := fmt.Sprintf("t%d", d.nextID)
	return Token{ID: id, Value: "v-" + id}, nil
}
func (d *recordingDriver) Revoke(_ context.Context, t Token) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.revoked = append(d.revoked, t.ID)
	if t.ID == d.failFor {
		return errors.New("revoke refused")
	}
	return nil
}

func newBroker(d Driver) *Broker {
	return New(d, fakeAuth{session: "ws/t/r-g1-0", token: "hb", id: Identity{Session: "ws/t/r-g1-0", Team: "t", Role: "r"}})
}

func TestIssueRefusesAnUnauthenticatedSeatBeforeTheDriverIsAsked(t *testing.T) {
	d := &recordingDriver{}
	b := newBroker(d)
	_, err := b.Issue(context.Background(), "ws/t/r-g1-0", "wrong")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if len(d.issued) != 0 {
		t.Fatalf("driver issued %d tokens for a refused seat", len(d.issued))
	}
}

func TestIssueAsksTheDriverForTheAuthenticatedIdentity(t *testing.T) {
	d := &recordingDriver{}
	b := newBroker(d)
	tok, err := b.Issue(context.Background(), "ws/t/r-g1-0", "hb")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if tok.Value == "" || tok.ID == "" {
		t.Fatalf("token = %+v, want an id and a value", tok)
	}
	want := Request{Team: "t", Role: "r", Session: "ws/t/r-g1-0"}
	if d.lastReq != want {
		t.Fatalf("driver request = %+v, want %+v", d.lastReq, want)
	}
}

func TestIssueReportsADriverFailure(t *testing.T) {
	d := &recordingDriver{issueErr: errors.New("idp down")}
	b := newBroker(d)
	if _, err := b.Issue(context.Background(), "ws/t/r-g1-0", "hb"); err == nil || !strings.Contains(err.Error(), "idp down") {
		t.Fatalf("err = %v, want the driver's error", err)
	}
}

func TestRevokeSessionRevokesEveryTokenIssuedToTheSeat(t *testing.T) {
	d := &recordingDriver{}
	b := newBroker(d)
	for range 2 {
		if _, err := b.Issue(context.Background(), "ws/t/r-g1-0", "hb"); err != nil {
			t.Fatalf("Issue: %v", err)
		}
	}
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if len(d.revoked) != 2 {
		t.Fatalf("revoked %v, want both tokens", d.revoked)
	}
	if err := b.RevokeSession(context.Background(), "ws/t/r-g1-0"); err != nil || len(d.revoked) != 2 {
		t.Fatalf("second revoke err=%v revoked=%v, want a no-op", err, d.revoked)
	}
}

func TestRevokeSessionLeavesOtherSeatsAlone(t *testing.T) {
	d := &recordingDriver{}
	b := New(d, multiAuth{})
	if _, err := b.Issue(context.Background(), "a", "ta"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Issue(context.Background(), "b", "tb"); err != nil {
		t.Fatal(err)
	}
	if err := b.RevokeSession(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if len(d.revoked) != 1 || d.revoked[0] != "t1" {
		t.Fatalf("revoked %v, want only the first seat's token t1", d.revoked)
	}
}

type multiAuth struct{}

func (multiAuth) Authenticate(key, token string) (Identity, error) {
	if token != "t"+key {
		return Identity{}, ErrUnauthorized
	}
	return Identity{Session: key, Team: "t", Role: "r"}, nil
}

func TestRevokeSessionRevokesTheRestWhenOneFails(t *testing.T) {
	d := &recordingDriver{failFor: "t1"}
	b := newBroker(d)
	for range 3 {
		if _, err := b.Issue(context.Background(), "ws/t/r-g1-0", "hb"); err != nil {
			t.Fatal(err)
		}
	}
	err := b.RevokeSession(context.Background(), "ws/t/r-g1-0")
	if err == nil || !strings.Contains(err.Error(), "revoke refused") {
		t.Fatalf("err = %v, want the failed revoke reported", err)
	}
	if len(d.revoked) != 3 {
		t.Fatalf("revoked %v, want all three attempted", d.revoked)
	}
}

func TestMockIssuesShortLivedTokensAndForgetsThemOnRevoke(t *testing.T) {
	m := NewMock(15*time.Minute, func() time.Time { return t0 })
	tok, err := m.Issue(context.Background(), Request{Team: "t", Role: "r", Session: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if !tok.Expires.Equal(t0.Add(15 * time.Minute)) {
		t.Fatalf("Expires = %v, want now plus the ttl", tok.Expires)
	}
	if got := m.Live(); len(got) != 1 || got[0] != tok.ID {
		t.Fatalf("Live = %v, want [%s]", got, tok.ID)
	}
	if err := m.Revoke(context.Background(), tok); err != nil {
		t.Fatal(err)
	}
	if got := m.Live(); len(got) != 0 {
		t.Fatalf("Live after revoke = %v, want none", got)
	}
	if m.Name() != "mock" {
		t.Fatalf("Name = %q, want mock", m.Name())
	}
}

func TestMockTokensDiffer(t *testing.T) {
	m := NewMock(time.Minute, func() time.Time { return t0 })
	a, _ := m.Issue(context.Background(), Request{Session: "s"})
	b, _ := m.Issue(context.Background(), Request{Session: "s"})
	if a.ID == b.ID || a.Value == b.Value || a.Value == "" {
		t.Fatalf("tokens %+v and %+v must differ and be non-empty", a, b)
	}
}

// secretSigner stands in for the host keystore. Its key bytes exist only here.
type secretSigner struct {
	secret []byte
	signed int
}

func (s *secretSigner) Sign(_ context.Context, _ string, _ []byte) ([]byte, error) {
	s.signed++
	return []byte("sig"), nil
}

func TestGenericOIDCHoldsTheKeyNameAndSignsNothingYet(t *testing.T) {
	s := &secretSigner{secret: []byte("host-keystore-private-bytes")}
	g := NewGenericOIDC("ai-agents-key", s)
	if g.Name() != "generic-oidc" {
		t.Fatalf("Name = %q, want generic-oidc", g.Name())
	}
	if _, err := g.Issue(context.Background(), Request{Session: "s"}); !errors.Is(err, ErrClaimShapeUndecided) {
		t.Fatalf("Issue err = %v, want ErrClaimShapeUndecided", err)
	}
	if err := g.Revoke(context.Background(), Token{ID: "x"}); !errors.Is(err, ErrClaimShapeUndecided) {
		t.Fatalf("Revoke err = %v, want ErrClaimShapeUndecided", err)
	}
	if s.signed != 0 {
		t.Fatalf("the stub asked the keystore to sign %d times", s.signed)
	}
	dump := fmt.Sprintf("%+v %#v", g, g)
	if !strings.Contains(dump, "ai-agents-key") {
		t.Fatalf("driver state %q does not carry the key name", dump)
	}
}

func TestNoKeyMaterialInBrokerOrDriverState(t *testing.T) {
	secret := []byte("host-keystore-private-bytes")
	s := &secretSigner{secret: secret}
	m := NewMock(time.Minute, func() time.Time { return t0 })
	b := newBroker(m)
	tok, err := b.Issue(context.Background(), "ws/t/r-g1-0", "hb")
	if err != nil {
		t.Fatal(err)
	}
	g := NewGenericOIDC("k", s)
	dump := fmt.Sprintf("%+v %#v %+v %#v %+v", b, b, m, m, tok) + fmt.Sprintf("%+v", g)
	for _, needle := range []string{string(secret), hex.EncodeToString(secret)} {
		if strings.Contains(dump, needle) {
			t.Fatalf("state carries key material %q", needle)
		}
	}
}
