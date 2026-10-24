package bus

import (
	"context"
	"errors"
	"time"
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

// ReadAuditRecord stub.
func ReadAuditRecord(_ context.Context, _, _, _ string, _ uint64) (AuditRecord, error) {
	return AuditRecord{}, errors.New("not implemented")
}
