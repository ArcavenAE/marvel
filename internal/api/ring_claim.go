package api

import (
	"fmt"
	"time"
)

// RingResult is where a ring stands. Pending and sent both hold the claim
// until RingAt + RingClaimWindow; failed does not.
type RingResult string

const (
	// RingPending is written before the keys are sent. It blocks too, which
	// covers a second ring racing the first send and a daemon that died
	// mid-send; after a restart a pending claim reads as rung.
	RingPending RingResult = "pending"
	// RingSent is written once the keys went in.
	RingSent RingResult = "sent"
	// RingFailed is written when the send errored; it does not block.
	RingFailed RingResult = "failed"
)

// RingClaimWindow is how long a ring holds its claim. It is section 8's confirm
// window until a measurement replaces it, and a var only so tests can move it.
var RingClaimWindow = 120 * time.Second

// RingClaim is the last ring written against a session through
// `marvel inject --ring`. Ringer is the daemon's account of the caller, a claim
// and not a proof. Nothing in it says the ring was seen or answered.
type RingClaim struct {
	RingAt time.Time  `json:"ring_at"`
	Ringer string     `json:"ringer"`
	Result RingResult `json:"result"`
}

// Blocks reports whether the claim holds the seat at now.
func (c RingClaim) Blocks(now time.Time) bool {
	if c.Result != RingPending && c.Result != RingSent {
		return false
	}
	return now.Before(c.RingAt.Add(RingClaimWindow))
}

// RingRefusal is the error a second ring gets inside the window.
type RingRefusal struct {
	Session string
	Holder  RingClaim
	Now     time.Time
}

func (e *RingRefusal) Error() string {
	return fmt.Sprintf("ring refused: %s was rung by %s at %s (%ds ago, window %ds)",
		e.Session, e.Holder.Ringer, e.Holder.RingAt.UTC().Format(time.RFC3339),
		int(e.Now.Sub(e.Holder.RingAt).Seconds()), int(RingClaimWindow.Seconds()))
}

// ClaimRing writes a pending claim for ringer, or returns a *RingRefusal naming
// the holder when a claim still blocks. The check and the write happen under
// the store lock, so two callers cannot both win.
func (s *Store) ClaimRing(key, ringer string, now time.Time) error {
	return s.UpdateSession(key, func(sess *Session) error {
		if held := sess.RingClaim; held != nil && held.Blocks(now) {
			return &RingRefusal{Session: key, Holder: *held, Now: now}
		}
		sess.RingClaim = &RingClaim{RingAt: now, Ringer: ringer, Result: RingPending}
		return nil
	})
}

// SettleRing records how the send ended, for the claim written at ringAt. A
// claim that has since been replaced is left alone.
func (s *Store) SettleRing(key string, ringAt time.Time, result RingResult) error {
	return s.UpdateSession(key, func(sess *Session) error {
		if sess.RingClaim == nil || !sess.RingClaim.RingAt.Equal(ringAt) {
			return nil
		}
		sess.RingClaim.Result = result
		return nil
	})
}
