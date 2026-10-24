package bus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// AuditRecord is one numbered record of the AGENT_AUDIT stream: the four
// headers the audit reader names, and the body as stored.
type AuditRecord struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Subject string    `json:"subject"`
	// Outcome, Stage and Refusal are the Director-Outcome, Director-Stage and
	// Director-Refusal headers; MsgID is Nats-Msg-Id. Empty when absent.
	Outcome string `json:"director_outcome,omitempty"`
	Stage   string `json:"director_stage,omitempty"`
	Refusal string `json:"director_refusal,omitempty"`
	MsgID   string `json:"nats_msg_id,omitempty"`
	Body    []byte `json:"body"`
}

// AuditMissKind says why a sequence number has no record.
type AuditMissKind int

const (
	AuditNoStream AuditMissKind = iota + 1
	AuditEmpty
	AuditAgedOut
	AuditPastEnd
	AuditGap
)

// AuditMiss is the answer for a number with no record. First and Last are the
// stream's sequence range when it holds records.
type AuditMiss struct {
	Kind        AuditMissKind
	Seq         uint64
	First, Last uint64
}

func (m *AuditMiss) Error() string { return "audit record missing" }

// ReadAuditRecord reads record seq of the AGENT_AUDIT stream as the broker's
// admin identity. It gets one message by sequence and then closes: nothing is
// published, acknowledged, deleted or purged, and no consumer is created. A
// number with no record comes back as an *AuditMiss; any other error is a
// failure to connect or to read.
func ReadAuditRecord(ctx context.Context, url, user, password string, seq uint64) (AuditRecord, error) {
	nc, err := connectAdmin(ctx, url, user, password)
	if err != nil {
		return AuditRecord{}, err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return AuditRecord{}, fmt.Errorf("jetstream context: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, structureRPC)
	defer cancel()

	stream, err := js.Stream(ctx, AuditStream)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return AuditRecord{}, &AuditMiss{Kind: AuditNoStream, Seq: seq}
	}
	if err != nil {
		return AuditRecord{}, fmt.Errorf("look up %s: %w", AuditStream, err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return AuditRecord{}, fmt.Errorf("read %s state: %w", AuditStream, err)
	}
	first, last := info.State.FirstSeq, info.State.LastSeq
	switch {
	case info.State.Msgs == 0:
		return AuditRecord{}, &AuditMiss{Kind: AuditEmpty, Seq: seq}
	case seq < first:
		return AuditRecord{}, &AuditMiss{Kind: AuditAgedOut, Seq: seq, First: first, Last: last}
	case seq > last:
		return AuditRecord{}, &AuditMiss{Kind: AuditPastEnd, Seq: seq, First: first, Last: last}
	}
	msg, err := stream.GetMsg(ctx, seq)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		return AuditRecord{}, &AuditMiss{Kind: AuditGap, Seq: seq, First: first, Last: last}
	}
	if err != nil {
		return AuditRecord{}, fmt.Errorf("get message %d of %s: %w", seq, AuditStream, err)
	}
	return AuditRecord{
		Seq:     msg.Sequence,
		Time:    msg.Time.UTC(),
		Subject: msg.Subject,
		Outcome: msg.Header.Get("Director-Outcome"),
		Stage:   msg.Header.Get("Director-Stage"),
		Refusal: msg.Header.Get("Director-Refusal"),
		MsgID:   msg.Header.Get("Nats-Msg-Id"),
		Body:    msg.Data,
	}, nil
}
