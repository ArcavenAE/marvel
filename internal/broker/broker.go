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
	"strings"
	"sync"
	"time"
)

// Signer is the host keystore as the broker sees it: sign this input with the
// key of this name. The key never crosses the interface.
type Signer interface {
	Sign(ctx context.Context, keyName string, input []byte) ([]byte, error)
	// Describe reports the public facts about the key of this name: what a
	// driver needs to build a client assertion's header and to publish the key.
	// It never returns private bytes, and it lists and creates nothing.
	Describe(ctx context.Context, keyName string) (KeyInfo, error)
}

// KeyInfo is what a keystore says about a key it holds. PublicKey is the
// public half only.
type KeyInfo struct {
	Algorithm string
	KeyID     string
	PublicKey []byte
}

// Request names the seat a token is for. The identity a provider trusts is the
// cluster's broker, never a seat. A driver puts Team and Role in the token as
// claims a provider policy can match, and Session only in the downstream
// session name and identifier, for audit. The seat is the authenticated one:
// the broker fills Session from the identity it checked, not from the key the
// caller typed.
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
	// ErrRevokeFailed is joined to the refusal of an ask whose seat ended
	// mid-ask when the token the driver had just issued could not be revoked.
	// The broker keeps the token for the seat's next revoke; a caller should
	// say so, because its own refusal hides a live credential.
	ErrRevokeFailed = errors.New("broker: could not revoke a token issued to a seat that ended")
)

// scrubbedError hides a token's value in an error a driver returned about it.
// A driver's error can echo the request, bearer value included, and callers
// log what the broker returns. The original stays reachable with errors.Is
// and errors.As.
//
// Only the exact value is replaced. A driver that echoes the value base64
// encoded, cut to a prefix or case-folded is not caught. If a provider is
// ever seen doing that, the fallback is to log the token id and the error
// type instead of the driver's text.
type scrubbedError struct {
	err   error
	value string
}

func (e *scrubbedError) Error() string {
	return strings.ReplaceAll(e.err.Error(), e.value, "[token value withheld]")
}

func (e *scrubbedError) Unwrap() error { return e.err }

// scrub wraps err so its text cannot carry t's value.
func scrub(t Token, err error) error {
	if err == nil || t.Value == "" {
		return err
	}
	return &scrubbedError{err: err, value: t.Value}
}

const (
	// DefaultTTL is how long a token lives unless a driver is told otherwise.
	// A token is short-lived on purpose: its TTL bounds every gap in revocation.
	DefaultTTL = 5 * time.Minute
	// MinTTL is the shortest a token may live. The seat's helper asks again at
	// about two thirds of a token's life and, with the daemon's socket absent,
	// retries for up to 90 s before it exits nonzero; it never prints a stale
	// token. A token shorter than this could expire before the helper can get
	// its replacement. The floor is judged from the moment the driver was
	// asked, not from its return, so a driver that stamps a token and then
	// stalls can hand one out with just under MinTTL left.
	MinTTL = 3 * time.Minute
	// MaxTTL is the longest a token may live. The broker refuses, and revokes,
	// a token a driver issues that outlives it.
	MaxTTL = 15 * time.Minute
)

// ErrTTLTooShort is returned when a driver issues a token that lives less than
// MinTTL. The broker revokes the token it was given.
var ErrTTLTooShort = errors.New("broker: the driver issued a token that expires before a helper can replace it")

// ErrTTLTooLong is returned when a driver issues a token with no expiry or one
// beyond MaxTTL. The broker revokes the token it was given.
var ErrTTLTooLong = errors.New("broker: the driver issued a token that outlives the ceiling")

// Broker issues tokens to authenticated seats and revokes them when the seat
// ends. A seat's helper asks again when a token expires; the daemon pushes no
// replacement into the seat.
type Broker struct {
	driver Driver
	auth   SessionAuth
	now    func() time.Time

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
		now:      time.Now,
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
	// The record hangs off the identity the broker checked, not the key the
	// caller typed. The two are the same exact lookup today; if they ever
	// differ, a revoke for the authenticated session must be seen by this ask
	// as well, so it is registered under that key too.
	sess := id.Session
	var a2 *asks
	var revokes2 int
	if sess != sessionKey {
		a2, revokes2 = b.begin(sess)
		defer b.finish(sess, a2)
	}
	// The floor is judged from before the driver was asked, so a driver that
	// issues exactly MinTTL is not refused for the milliseconds it took.
	askedAt := b.now()
	tok, err := b.driver.Issue(ctx, Request{Team: id.Team, Role: id.Role, Session: sess})
	if err != nil {
		return Token{}, fmt.Errorf("broker: %s driver: %w", b.driver.Name(), err)
	}
	if tok.Expires.IsZero() || tok.Expires.After(b.now().Add(MaxTTL)) {
		return Token{}, b.refuse(ctx, sess, tok, ErrTTLTooLong)
	}
	if tok.Expires.Before(askedAt.Add(MinTTL)) {
		return Token{}, b.refuse(ctx, sess, tok, ErrTTLTooShort)
	}
	b.mu.Lock()
	if a.revokes != revokesAtStart || (a2 != nil && a2.revokes != revokes2) {
		b.mu.Unlock()
		return Token{}, b.refuse(ctx, sess, tok, ErrUnauthorized)
	}
	b.issued[sess] = append(b.issued[sess], tok)
	b.mu.Unlock()
	return tok, nil
}

// refuse revokes a token the broker will not hand out and returns cause. If
// the revoke fails the token is kept under the session, so the next revoke or
// the daemon's retry pass takes it, and the error says so. The caller's
// context may be what ended the ask; the revoke must not be.
func (b *Broker) refuse(ctx context.Context, sess string, tok Token, cause error) error {
	rerr := b.driver.Revoke(context.WithoutCancel(ctx), tok)
	if rerr == nil {
		return cause
	}
	b.mu.Lock()
	b.issued[sess] = append(b.issued[sess], tok)
	b.mu.Unlock()
	return fmt.Errorf("%w: %w: token %s: %w", cause, ErrRevokeFailed, tok.ID, scrub(tok, rerr))
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
	return b.revokeKept(ctx, sessionKey, toks)
}

// revokeKept revokes toks, tokens already taken off the books for the key, and
// puts back the ones the driver would not revoke so a later pass retries them.
func (b *Broker) revokeKept(ctx context.Context, sessionKey string, toks []Token) error {
	var errs []error
	var failed []Token
	for _, t := range toks {
		if err := b.driver.Revoke(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("revoke %s: %w", t.ID, scrub(t, err)))
			failed = append(failed, t)
		}
	}
	if len(failed) > 0 {
		// A token the driver would not revoke is still live. Keep it for the
		// next revoke for this seat.
		b.mu.Lock()
		b.issued[sessionKey] = append(failed, b.issued[sessionKey]...)
		b.mu.Unlock()
	}
	return errors.Join(errs...)
}

// takeKept takes the tokens with the given ids off the books for the key and
// returns them. A token issued since the ids were read is left alone.
func (b *Broker) takeKept(sessionKey string, ids map[string]bool) []Token {
	b.mu.Lock()
	defer b.mu.Unlock()
	var taken, left []Token
	for _, t := range b.issued[sessionKey] {
		if ids[t.ID] {
			taken = append(taken, t)
		} else {
			left = append(left, t)
		}
	}
	if len(left) == 0 {
		delete(b.issued, sessionKey)
	} else {
		b.issued[sessionKey] = left
	}
	return taken
}

// RetryRevokes revokes the kept tokens of every session key for which exists
// reports no session. A revoke that failed when a seat ended leaves its tokens
// kept, and once the seat's row is dropped nothing else reports the key again.
// Tokens that still fail stay kept for the next pass.
//
// Names are reused after a drop, so a seat can take a key between the moment
// exists answers and the revoke. The pass therefore reads the kept token ids
// first, asks exists with no lock held, and revokes only those ids: a token
// issued afterwards belongs to the new seat and is left alone, and the pass
// does not count as a revoke of an ask that is in flight for the key.
func (b *Broker) RetryRevokes(ctx context.Context, exists func(sessionKey string) bool) error {
	b.mu.Lock()
	kept := make(map[string]map[string]bool, len(b.issued))
	for k, toks := range b.issued {
		if len(toks) == 0 {
			continue
		}
		ids := make(map[string]bool, len(toks))
		for _, t := range toks {
			ids[t.ID] = true
		}
		kept[k] = ids
	}
	b.mu.Unlock()
	keys := make([]string, 0, len(kept))
	for k := range kept {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var errs []error
	for _, k := range keys {
		if exists(k) {
			continue
		}
		if err := b.revokeKept(ctx, k, b.takeKept(k, kept[k])); err != nil {
			errs = append(errs, err)
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

// Key asks the keystore to describe the key this driver names. It is the
// public description only, and asks the keystore nothing else.
func (g *GenericOIDC) Key(ctx context.Context) (KeyInfo, error) {
	info, err := g.signer.Describe(ctx, g.keyName)
	if err != nil {
		return KeyInfo{}, fmt.Errorf("broker: describe key %q: %w", g.keyName, err)
	}
	return info, nil
}

// Issue is not built until the claim shape is ruled.
func (g *GenericOIDC) Issue(context.Context, Request) (Token, error) {
	return Token{}, ErrClaimShapeUndecided
}

// Revoke is not built until the claim shape is ruled.
func (g *GenericOIDC) Revoke(context.Context, Token) error { return ErrClaimShapeUndecided }
