package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
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
