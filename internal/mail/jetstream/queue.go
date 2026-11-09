// Package jetstream is the NATS JetStream adapter for mail.Queue (A3 of
// docs/design/marvel-watcher.md section 11.3). It is the only package under
// internal/mail that imports NATS, and only the daemon's start-up wiring
// constructs it; internal/mail, internal/turn and internal/watcher never import
// it (the boundary test in internal/mail enforces that).
package jetstream

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	njs "github.com/nats-io/nats.go/jetstream"

	"github.com/arcavenae/marvel/internal/mail"
)

// DefaultBound is how long one broker read may take before it is abandoned and
// the tier reads unknown. It is the bound director#399 puts on its own reads.
const DefaultBound = 500 * time.Millisecond

// Queue answers mail.Queue from a JetStream connection. It only reads: it lists
// consumers, reads one message by sequence and subject, and asks for stream
// counts. It never creates a consumer, fetches or acks a message.
type Queue struct {
	js       njs.JetStream
	presence Presence
	bound    time.Duration
}

// NewQueue returns a Queue over js, one handle per tier (the global tier is
// another domain), with DefaultBound on every read. presence says where the
// tier's sessions announce themselves; a durable is live while its session has
// a key there.
func NewQueue(js njs.JetStream, presence Presence) *Queue {
	return &Queue{js: js, presence: presence, bound: DefaultBound}
}

func (q *Queue) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, q.bound)
}

// Durables lists the consumers of stream. At is the latest TimeStamp any
// consumer's info carries, which is the broker's own clock; it is zero when the
// stream has no consumer or the server left it unset, and an age then runs on
// the reader's clock (section 11.2).
func (q *Queue) Durables(ctx context.Context, stream string) (mail.Durables, error) {
	ctx, cancel := q.bounded(ctx)
	defer cancel()
	st, err := q.js.Stream(ctx, stream)
	if err != nil {
		return mail.Durables{}, fmt.Errorf("stream %s: %w", stream, err)
	}
	live, err := q.presence.live(ctx, q.js)
	if err != nil {
		return mail.Durables{}, err
	}
	var out mail.Durables
	lister := st.ListConsumers(ctx)
	for ci := range lister.Info() {
		d := durableOf(ci)
		d.Live = live[q.presence.sessionOf(d.Name)]
		out.List = append(out.List, d)
		if ci.TimeStamp.After(out.At) {
			out.At = ci.TimeStamp
		}
	}
	if err := lister.Err(); err != nil {
		return mail.Durables{}, fmt.Errorf("list consumers of %s: %w", stream, err)
	}
	return out, nil
}

func durableOf(ci *njs.ConsumerInfo) mail.Durable {
	filters := ci.Config.FilterSubjects
	if len(filters) == 0 && ci.Config.FilterSubject != "" {
		filters = []string{ci.Config.FilterSubject}
	}
	d := mail.Durable{
		Name:       ci.Name,
		Filters:    slices.Clone(filters),
		Pending:    int64(ci.NumPending),
		AckPending: int64(ci.NumAckPending),
		AckFloor:   ci.AckFloor.Stream,
	}
	if ci.AckFloor.Last != nil {
		d.LastAck = *ci.AckFloor.Last
	}
	return d
}

// Oldest returns the first message above seq on any of subjects. The broker
// answers one subject at a time ("the next message for this subject from this
// sequence"), so each subject is asked and the lowest sequence wins.
func (q *Queue) Oldest(ctx context.Context, stream string, above uint64, subjects []string) (mail.Msg, bool, error) {
	ctx, cancel := q.bounded(ctx)
	defer cancel()
	st, err := q.js.Stream(ctx, stream)
	if err != nil {
		return mail.Msg{}, false, fmt.Errorf("stream %s: %w", stream, err)
	}
	var best mail.Msg
	found := false
	for _, subj := range dedupe(subjects) {
		m, err := st.GetMsg(ctx, above+1, njs.WithGetMsgSubject(subj))
		if errors.Is(err, njs.ErrMsgNotFound) {
			continue
		}
		if err != nil {
			return mail.Msg{}, false, fmt.Errorf("first message above %d on %s: %w", above, subj, err)
		}
		if !found || m.Sequence < best.Seq {
			best, found = mail.Msg{Seq: m.Sequence, Stored: m.Time}, true
		}
	}
	return best, found, nil
}

// Count returns how many messages stream holds on subjects, from the stream's
// own per-subject count, each distinct subject once.
func (q *Queue) Count(ctx context.Context, stream string, subjects []string) (int64, error) {
	ctx, cancel := q.bounded(ctx)
	defer cancel()
	st, err := q.js.Stream(ctx, stream)
	if err != nil {
		return 0, fmt.Errorf("stream %s: %w", stream, err)
	}
	var n int64
	for _, subj := range dedupe(subjects) {
		info, err := st.Info(ctx, njs.WithSubjectFilter(subj))
		if err != nil {
			return 0, fmt.Errorf("count on %s: %w", subj, err)
		}
		n += int64(info.State.Subjects[subj])
	}
	return n, nil
}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
