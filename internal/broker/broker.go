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
	"errors"
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
type Broker struct{}

// New returns a broker that issues through d for seats a authenticates.
func New(d Driver, a SessionAuth) *Broker { return &Broker{} }

// Issue authenticates the seat, then asks the driver for a token.
func (b *Broker) Issue(ctx context.Context, sessionKey, heartbeatToken string) (Token, error) {
	return Token{}, nil
}

// RevokeSession revokes every token issued to the seat. It revokes the rest
// when one revoke fails, and reports every failure.
func (b *Broker) RevokeSession(ctx context.Context, sessionKey string) error { return nil }

// Mock is a driver that issues opaque tokens and tracks them, for tests and
// for a cluster with no identity provider. It signs nothing.
type Mock struct{}

// NewMock returns a mock driver whose tokens live for ttl.
func NewMock(ttl time.Duration, now func() time.Time) *Mock { return &Mock{} }

// Name is the driver's registered name.
func (m *Mock) Name() string { return "" }

// Issue returns an opaque token for the seat.
func (m *Mock) Issue(ctx context.Context, r Request) (Token, error) { return Token{}, nil }

// Revoke forgets the token.
func (m *Mock) Revoke(ctx context.Context, t Token) error { return nil }

// Live returns the ids of the tokens not yet revoked.
func (m *Mock) Live() []string { return nil }

// GenericOIDC will sign a client assertion through the host keystore. It
// holds the key's name and a Signer, nothing else.
type GenericOIDC struct{}

// NewGenericOIDC returns the stub driver for the keystore key named keyName.
func NewGenericOIDC(keyName string, s Signer) *GenericOIDC { return &GenericOIDC{} }

// Name is the driver's registered name.
func (g *GenericOIDC) Name() string { return "" }

// Issue is not built until the claim shape is ruled.
func (g *GenericOIDC) Issue(ctx context.Context, r Request) (Token, error) { return Token{}, nil }

// Revoke is not built until the claim shape is ruled.
func (g *GenericOIDC) Revoke(ctx context.Context, t Token) error { return nil }
