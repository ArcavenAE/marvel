// Package broker is the skeleton of marvel's credential broker: a seat asks
// for a short-lived token, the broker checks the seat is who its heartbeat
// token says, and a driver issues the token. The broker holds no signing
// key. A driver that must sign names a key in the host keystore and asks the
// keystore to sign, so marvel knows a key's name and never its bytes
// (ADR-009, SOUL section 3).
//
// Only the mock driver issues anything today. The generic-oidc driver is a
// stub until its claim shape is ruled.
package broker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Signer is the host keystore as the broker sees it: sign this input with the
// key of this name. The key never crosses the interface.
type Signer interface {
	Sign(ctx context.Context, keyName string, input []byte) ([]byte, error)
}

// Request names the seat a token is for. A driver puts these in the token's
// downstream session name and its identifier, not in the identity itself.
type Request struct {
	Team    string
	Role    string
	Session string
}

// Token is one issued credential. Value is the bearer string a seat's helper
// prints; ID names the token so it can be revoked.
type Token struct {
	ID      string
	Value   string
	Expires time.Time
}

// Driver issues and revokes tokens for one kind of identity provider.
type Driver interface {
	Name() string
	Issue(ctx context.Context, r Request) (Token, error)
	Revoke(ctx context.Context, t Token) error
}

// Identity is a seat the broker has authenticated.
type Identity struct {
	Session string
	Team    string
	Role    string
}

// SessionAuth checks a heartbeat token against a live session.
type SessionAuth interface {
	Authenticate(sessionKey, heartbeatToken string) (Identity, error)
}

var (
	// ErrUnauthorized is returned when the heartbeat token does not belong to
	// a live session.
	ErrUnauthorized = errors.New("broker: heartbeat token does not belong to a live session")
	// ErrClaimShapeUndecided is returned by the generic-oidc driver until the
	// claim shape it signs is ruled.
	ErrClaimShapeUndecided = errors.New("broker: generic-oidc claim shape is not decided")
)

// Broker issues tokens to authenticated seats and revokes them when the seat
// ends.
type Broker struct {
	driver Driver
	auth   SessionAuth

	mu       sync.Mutex
	issued   map[string][]Token // by session key
	inflight map[string]*asks   // by session key, only while an ask is running
}

// asks counts the Issue calls running for one session key and the revokes
// that landed while any of them ran. The entry exists only while a call is
// running, so the map does not grow with the sessions a daemon has seen.
type asks struct {
	running int
	revokes int
}

// New returns a broker that issues through d for seats a authenticates.
func New(d Driver, a SessionAuth) *Broker {
	return &Broker{
		driver:   d,
		auth:     a,
		issued:   make(map[string][]Token),
		inflight: make(map[string]*asks),
	}
}

// begin registers an ask for the key and returns the revoke count it started
// at. It runs before the seat is authenticated, so a revoke at any later
// point is counted against this ask.
func (b *Broker) begin(key string) (*asks, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.inflight[key]
	if a == nil {
		a = &asks{}
		b.inflight[key] = a
	}
	a.running++
	return a, a.revokes
}

func (b *Broker) finish(key string, a *asks) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if a.running--; a.running == 0 {
		delete(b.inflight, key)
	}
}

// Issue authenticates the seat, then asks the driver for a token. If the
// seat's end is reported while the driver is issuing, the token is revoked
// again and the ask is refused: the revoke had nothing to find, so nothing
// else would revoke it.
func (b *Broker) Issue(ctx context.Context, sessionKey, heartbeatToken string) (Token, error) {
	a, revokesAtStart := b.begin(sessionKey)
	defer b.finish(sessionKey, a)

	id, err := b.auth.Authenticate(sessionKey, heartbeatToken)
	if err != nil {
		return Token{}, err
	}
	tok, err := b.driver.Issue(ctx, Request{Team: id.Team, Role: id.Role, Session: id.Session})
	if err != nil {
		return Token{}, fmt.Errorf("broker: %s driver: %w", b.driver.Name(), err)
	}
	b.mu.Lock()
	if a.revokes != revokesAtStart {
		b.mu.Unlock()
		// The caller's context may be what ended the ask; the revoke must not be.
		if rerr := b.driver.Revoke(context.WithoutCancel(ctx), tok); rerr != nil {
			return Token{}, fmt.Errorf("%w: seat ended during the ask; revoke %s: %w", ErrUnauthorized, tok.ID, rerr)
		}
		return Token{}, ErrUnauthorized
	}
	b.issued[sessionKey] = append(b.issued[sessionKey], tok)
	b.mu.Unlock()
	return tok, nil
}

// RevokeSession revokes every token issued to the seat. It revokes the rest
// when one revoke fails, and reports every failure.
func (b *Broker) RevokeSession(ctx context.Context, sessionKey string) error {
	b.mu.Lock()
	toks := b.issued[sessionKey]
	delete(b.issued, sessionKey)
	if a := b.inflight[sessionKey]; a != nil {
		a.revokes++
	}
	b.mu.Unlock()
	var errs []error
	for _, t := range toks {
		if err := b.driver.Revoke(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("revoke %s: %w", t.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Mock is a driver that issues opaque tokens and tracks them, for tests and
// for a cluster with no identity provider. It signs nothing.
type Mock struct {
	ttl time.Duration
	now func() time.Time

	mu   sync.Mutex
	live map[string]bool
}

// NewMock returns a mock driver whose tokens live for ttl.
func NewMock(ttl time.Duration, now func() time.Time) *Mock {
	if now == nil {
		now = time.Now
	}
	return &Mock{ttl: ttl, now: now, live: make(map[string]bool)}
}

// Name is the driver's registered name.
func (m *Mock) Name() string { return "mock" }

// Issue returns an opaque token for the seat.
func (m *Mock) Issue(_ context.Context, r Request) (Token, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Token{}, fmt.Errorf("mint mock token: %w", err)
	}
	id := hex.EncodeToString(b[:8])
	t := Token{ID: id, Value: "mock." + id + "." + hex.EncodeToString(b[8:]), Expires: m.now().Add(m.ttl)}
	m.mu.Lock()
	m.live[id] = true
	m.mu.Unlock()
	return t, nil
}

// Revoke forgets the token.
func (m *Mock) Revoke(_ context.Context, t Token) error {
	m.mu.Lock()
	delete(m.live, t.ID)
	m.mu.Unlock()
	return nil
}

// Live returns the ids of the tokens not yet revoked.
func (m *Mock) Live() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.live))
	for id := range m.live {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GenericOIDC will sign a client assertion through the host keystore. It
// holds the key's name and a Signer, nothing else.
type GenericOIDC struct {
	keyName string
	signer  Signer
}

// NewGenericOIDC returns the stub driver for the keystore key named keyName.
func NewGenericOIDC(keyName string, s Signer) *GenericOIDC {
	return &GenericOIDC{keyName: keyName, signer: s}
}

// Name is the driver's registered name.
func (g *GenericOIDC) Name() string { return "generic-oidc" }

// Issue is not built until the claim shape is ruled.
func (g *GenericOIDC) Issue(context.Context, Request) (Token, error) {
	return Token{}, ErrClaimShapeUndecided
}

// Revoke is not built until the claim shape is ruled.
func (g *GenericOIDC) Revoke(context.Context, Token) error { return ErrClaimShapeUndecided }
