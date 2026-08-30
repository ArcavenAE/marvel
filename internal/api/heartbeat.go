package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// HeartbeatTokenEnv is the environment variable adapters inject the
// session's heartbeat token into. Environment rather than a command-line
// flag: an argv is readable from the process table, and every agent on
// the host can list it.
const HeartbeatTokenEnv = "MARVEL_HEARTBEAT_TOKEN"

// ErrHeartbeatUnauthorized is returned when a heartbeat presents no token
// or the wrong one for the session it claims.
var ErrHeartbeatUnauthorized = errors.New("heartbeat token does not match the session it claims")

// HeartbeatAuth reports how a heartbeat was admitted. The daemon renders
// it as an event so the unbound case is audible rather than assumed.
type HeartbeatAuth string

const (
	// HeartbeatAuthToken is a heartbeat that presented the token minted
	// for the session it claims.
	HeartbeatAuthToken HeartbeatAuth = "token"
	// HeartbeatAuthUnbound is a heartbeat admitted against a session
	// record carrying no token hash. Only records written before the
	// token existed are in that state, so the exemption drains as those
	// sessions end; a session this daemon spawned always carries one.
	HeartbeatAuthUnbound HeartbeatAuth = "unbound"
)

// NewHeartbeatToken mints a 256-bit token, hex-encoded, and returns it
// with its digest. Minted once per session at spawn.
func NewHeartbeatToken() (token, hash string, err error) {
	var b [32]byte
	if _, rerr := rand.Read(b[:]); rerr != nil {
		return "", "", fmt.Errorf("mint heartbeat token: %w", rerr)
	}
	token = hex.EncodeToString(b[:])
	return token, HashHeartbeatToken(token), nil
}

// HashHeartbeatToken returns the hex-encoded SHA-256 of a token. The
// empty token has no hash: an absent secret must not acquire a digest a
// caller could then match against.
func HashHeartbeatToken(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// authenticateHeartbeat checks a presented token against a session's
// stored hash. Callers hold the store lock.
func authenticateHeartbeat(sess *Session, token string) (HeartbeatAuth, error) {
	if sess.HeartbeatTokenHash == "" {
		// The one deliberate fail-open, and it is bounded rather than a
		// policy: a record with no hash was written by a binary that
		// minted no token, so its agent has no token to present and
		// refusing would restart a live fleet on upgrade. Every session
		// spawned from here on carries a hash, so the exemption cannot
		// widen, and the daemon emits heartbeat.unbound each time it
		// applies.
		return HeartbeatAuthUnbound, nil
	}
	presented := HashHeartbeatToken(token)
	if subtle.ConstantTimeCompare([]byte(presented), []byte(sess.HeartbeatTokenHash)) != 1 {
		return "", ErrHeartbeatUnauthorized
	}
	return HeartbeatAuthToken, nil
}

// HeartbeatRequest is the heartbeat RPC's wire shape, shared by every
// producer and by the daemon that consumes it. One type rather than a
// map literal per forwarder, so a field rename is a compile error at all
// of them instead of a missing key at runtime.
//
// It exists because the map-literal version failed exactly that way.
// PR #168 added SessionToken and updated the two forwarders it could see;
// PR #170 added a third in the same night, built against the base before
// the field existed. Nothing failed to compile, both suites passed, and
// codex context pressure was dead on the merge commit. See
// finding-023.
type HeartbeatRequest struct {
	SessionKey string `json:"session_key"`
	// SessionToken is the secret marvel minted for this session at spawn
	// and injected into its environment as MARVEL_HEARTBEAT_TOKEN. It is
	// what binds the reading below to the session named above; the key
	// alone is public, guessable from `marvel get sessions`, and every
	// agent on the host can reach this socket.
	SessionToken   string  `json:"session_token,omitempty"`
	ContextPercent float64 `json:"context_percent"`
	// Model is the model as the reporter names it, "" when the reporter
	// does not know (the simulator). The statusline feed sends the
	// harness's display name.
	Model string `json:"model,omitempty"`
	// ContextTokens is the NUMERATOR: the occupancy the harness measured,
	// in tokens, 0 when the producer ships only a percentage. A producer
	// that ships it turns this RPC from a bare percentage into a graded
	// reading — see the pair with ContextWindow below and aae-orc-38yr.
	//
	// It is the harness's own occupancy figure (input + cache classes,
	// output excluded, mirroring the harness's own arithmetic), NOT
	// re-derived here. Zero is "not reported", never "empty context".
	ContextTokens int `json:"context_tokens,omitempty"`
	// ContextWindow is the DENOMINATOR: the window the harness declared on
	// its cooperative feed, 0 when the payload declared none. When it
	// arrives WITH a numerator, UpdateSessionHeartbeat routes it and the
	// session's manifest limit through usage.Resolve as a usage.LimitFromFeed
	// rung (below an operator's manifest override, above the shipped table),
	// and derives the percentage against the window that resolves.
	//
	// A numerator is required: a window with no ContextTokens is left
	// unconsumed (the reading stays percentage-only), because there is
	// nothing to divide by it. Paired with ContextTokens it lets a consumer
	// DERIVE the percentage against the denominator marvel trusts, rather
	// than accept a bare figure the harness computed against its own window.
	// It was the producer half of a seam whose consumer did not exist
	// (finding-023); aae-orc-38yr built the consumer.
	ContextWindow int `json:"context_window,omitempty"`
}

// NewHeartbeatRequest builds a heartbeat, reading the session token from
// the environment marvel constructed at spawn.
//
// The token is deliberately NOT a parameter. Every forwarder gets it from
// the same variable, and a parameter is a thing a fourth forwarder can
// forget to pass; there is nothing here to omit. Tests and any caller
// holding a token by other means use NewHeartbeatRequestWithToken.
func NewHeartbeatRequest(sessionKey string, contextPercent float64, model string) HeartbeatRequest {
	return NewHeartbeatRequestWithToken(sessionKey, os.Getenv(HeartbeatTokenEnv), contextPercent, model)
}

// NewHeartbeatRequestWithToken is NewHeartbeatRequest with the token
// supplied rather than read from the environment.
//
// An empty token is passed through as an empty field rather than being
// rejected: a session spawned before tokens existed has none to present,
// and authenticateHeartbeat admits that case and says so on the event
// ring. Sending a placeholder instead would hash to a value matching no
// record and convert that documented fail-open into a refusal.
func NewHeartbeatRequestWithToken(sessionKey, token string, contextPercent float64, model string) HeartbeatRequest {
	return HeartbeatRequest{
		SessionKey:     sessionKey,
		SessionToken:   token,
		ContextPercent: contextPercent,
		Model:          model,
	}
}
