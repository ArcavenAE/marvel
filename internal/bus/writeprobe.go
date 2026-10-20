package bus

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The write probe (aae-orc-kjix3, marvel#358 section 5). Every structural
// check reads: it looks an object up and reads /varz. A store that has run
// out of space answers every one of those, so the 2026-09-24 hub wedge
// (11,530 "no space left on device" store errors over 24 hours) passed
// them all. The probe writes a canary to each declared service-scope
// object and removes it again, so a store that refuses writes reads as one.
//
// It reports and never acts on the broker: no restart, no hold on spawns.
// A restart drops every live connection and does not free a disk.

const (
	// probeKey is the canary key in a declared bucket.
	probeKey = "marvel.write-probe"
	// probeToken stands in for each wildcard of a stream's subject when the
	// canary subject is built, so the canary matches the stream and nothing
	// that addresses a real seat.
	probeToken = "marvel-probe"
)

// probeRPC bounds one object's canary write, so a store that hangs a write
// reads as failed and not as a stuck tick.
var probeRPC = 10 * time.Second

// WriteFailure is one declared object whose canary write failed.
type WriteFailure struct {
	Object string `json:"object"`
	Err    string `json:"err"`
}

// WriteSkip is one declared object the probe did not write to, because the
// canary would have evicted or been refused by a real limit. It is not a
// failure and not a pass: the object was not tested.
type WriteSkip struct {
	Object string `json:"object"`
	Reason string `json:"reason"`
}

// WriteReading is one write-probe reading of a managed broker.
type WriteReading struct {
	Failures []WriteFailure `json:"failures,omitempty"`
	Skipped  []WriteSkip    `json:"skipped,omitempty"`
}

// Problems renders the failing objects, sorted and comma separated, for the
// status and the event. It doubles as the change key: the same string means
// the same set, whatever the server said this time.
func (w WriteReading) Problems() string {
	names := make([]string, 0, len(w.Failures))
	for _, f := range w.Failures {
		names = append(names, f.Object)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// SkippedNames renders the objects the probe did not test, sorted and comma
// separated.
func (w WriteReading) SkippedNames() string {
	names := make([]string, 0, len(w.Skipped))
	for _, sk := range w.Skipped {
		names = append(names, sk.Object)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// detail is the failing objects with the server's words, for the event.
func (w WriteReading) detail() string {
	parts := make([]string, 0, len(w.Failures))
	for _, f := range w.Failures {
		parts = append(parts, f.Object+": "+f.Err)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// ProbeWrites writes and removes a canary in every service-scope stream and
// bucket the broker carries. A refused or timed-out write is a failure in
// the reading. A broker that cannot be reached, or an object that cannot
// even be looked up, is an error: no information, and the caller keeps its
// last reading. An object that is absent is skipped, because the structural
// check owns that miss.
func ProbeWrites(ctx context.Context, url, user, password string) (WriteReading, error) {
	var out WriteReading
	nc, err := connectAdmin(ctx, url, user, password)
	if err != nil {
		return out, err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return out, fmt.Errorf("jetstream context: %w", err)
	}
	nonce := []byte(fmt.Sprintf("marvel write probe %d", time.Now().UnixNano()))
	for _, obj := range DeclaredObjects() {
		if obj.Scope != ScopeService {
			continue
		}
		octx, cancel := context.WithTimeout(ctx, probeRPC)
		var failure error
		switch obj.Kind {
		case ObjectKV:
			kv, kerr := js.KeyValue(octx, obj.Name)
			switch {
			case errors.Is(kerr, jetstream.ErrBucketNotFound):
				cancel()
				continue
			case kerr != nil:
				cancel()
				return out, fmt.Errorf("look up %s: %w", obj.Name, kerr)
			}
			// A bucket is a stream underneath, and a put plus a delete marker
			// is two messages in it.
			backing, berr := js.Stream(octx, "KV_"+obj.Name)
			if berr != nil {
				cancel()
				return out, fmt.Errorf("look up the stream behind %s: %w", obj.Name, berr)
			}
			reason, rerr := capReason(octx, backing, 2, len(nonce))
			if rerr != nil {
				cancel()
				return out, fmt.Errorf("read limits of %s: %w", obj.Name, rerr)
			}
			if reason != "" {
				cancel()
				out.Skipped = append(out.Skipped, WriteSkip{Object: obj.Name, Reason: reason})
				continue
			}
			failure = probeBucket(octx, kv, nonce)
		case ObjectStream:
			st, serr := js.Stream(octx, obj.Name)
			switch {
			case errors.Is(serr, jetstream.ErrStreamNotFound):
				cancel()
				continue
			case serr != nil:
				cancel()
				return out, fmt.Errorf("look up %s: %w", obj.Name, serr)
			}
			if obj.Stream == nil || len(obj.Stream.Subjects) == 0 {
				cancel()
				return out, fmt.Errorf("declared stream %s has no subject to probe", obj.Name)
			}
			reason, rerr := capReason(octx, st, 1, len(nonce))
			if rerr != nil {
				cancel()
				return out, fmt.Errorf("read limits of %s: %w", obj.Name, rerr)
			}
			if reason != "" {
				cancel()
				out.Skipped = append(out.Skipped, WriteSkip{Object: obj.Name, Reason: reason})
				continue
			}
			failure = probeStream(octx, js, st, canarySubject(obj.Stream.Subjects[0]), nonce)
		default:
			cancel()
			return out, fmt.Errorf("declared object %s has unknown kind %q", obj.Name, obj.Kind)
		}
		cancel()
		if failure != nil {
			out.Failures = append(out.Failures, WriteFailure{Object: obj.Name, Err: clip(failure.Error())})
		}
	}
	return out, nil
}

// probeOverhead is a margin for a message's subject and framing when the
// probe asks whether its writes would reach a byte cap.
const probeOverhead = 256

// capReason reads the stream's live config and state, and says why a canary of
// msgs messages carrying size bytes each should not be written: the write
// would evict a real message under discard-old, or be refused under
// discard-new. It returns "" when there is room. Declared streams carry no
// caps today, but an operator can set them on a live broker, and the probe
// must never make its own writes the cause of data loss.
func capReason(ctx context.Context, st jetstream.Stream, msgs, size int) (string, error) {
	info, err := st.Info(ctx)
	if err != nil {
		return "", err
	}
	c := info.Config
	if c.MaxMsgs > 0 && info.State.Msgs+uint64(msgs) > uint64(c.MaxMsgs) {
		return fmt.Sprintf("at its message cap (%d of %d); a canary would evict or be refused", info.State.Msgs, c.MaxMsgs), nil
	}
	if c.MaxBytes > 0 && info.State.Bytes+uint64(msgs*(size+probeOverhead)) > uint64(c.MaxBytes) {
		return fmt.Sprintf("within one canary of its byte cap (%d of %d bytes); a canary would evict or be refused", info.State.Bytes, c.MaxBytes), nil
	}
	return "", nil
}

// probeBucket puts, reads back and deletes the canary key.
func probeBucket(ctx context.Context, kv jetstream.KeyValue, nonce []byte) error {
	if _, err := kv.Put(ctx, probeKey, nonce); err != nil {
		return fmt.Errorf("put: %w", err)
	}
	got, err := kv.Get(ctx, probeKey)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if string(got.Value()) != string(nonce) {
		return errors.New("get: the canary read back with a different value")
	}
	if err := kv.Delete(ctx, probeKey); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// probeStream publishes the canary with an ack, then removes that one
// message by sequence so the stream keeps nothing of the probe.
func probeStream(ctx context.Context, js jetstream.JetStream, st jetstream.Stream, subject string, nonce []byte) error {
	ack, err := js.Publish(ctx, subject, nonce)
	if err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	if err := st.DeleteMsg(ctx, ack.Sequence); err != nil {
		return fmt.Errorf("delete message %d: %w", ack.Sequence, err)
	}
	return nil
}

// canarySubject turns a stream's subject filter into a concrete subject
// inside it: every wildcard token becomes probeToken.
func canarySubject(filter string) string {
	toks := strings.Split(filter, ".")
	for i, t := range toks {
		if t == "*" || t == ">" {
			toks[i] = probeToken
		}
	}
	return strings.Join(toks, ".")
}

// clip keeps a server error short enough for an event line.
func clip(s string) string {
	const max = 200
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
