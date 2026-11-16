package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// withCostRate adds the statusline dollar rate to a described session as
// cost_rate. The rate has no column until the dollar-rate slice, so describe
// carries it (card i477c1113-2, read by director as option 1). A seat with no
// cost total carries none.
func withCostRate(raw []byte, now time.Time) ([]byte, error) {
	if !isJSONObject(raw) {
		return raw, nil
	}
	var s api.Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("decode session for cost rate: %w", err)
	}
	v, ok := costRate(s, now)
	if !ok {
		return raw, nil
	}
	return withField(raw, "cost_rate", v)
}

// tokenSourceNote says where a seat's token sum comes from and what it leaves
// out, so a "-" in RATE and a "+" on TOUT can be read.
const tokenSourceNote = "output tokens summed from the seat's claude transcript, main file only; subagents are excluded. " +
	"A + marks a lower bound: the reader began at the file's end, or the file shrank or changed. " +
	"The per-message figures are in memory, so the reader resets with the daemon, which marks the sum +. " +
	"RATE is a dash on this source: the rate is not computed from the transcript in this release."

// withTokenSource adds the note to a described session that has a token sum.
func withTokenSource(raw []byte) ([]byte, error) {
	if !isJSONObject(raw) {
		return raw, nil
	}
	var s api.Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("decode session for token source: %w", err)
	}
	if s.SpendOut == nil {
		return raw, nil
	}
	return withField(raw, "token_source", tokenSourceNote)
}

// withField adds one string key to a JSON object reply.
func withField(raw []byte, key, value string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode reply for %s: %w", key, err)
	}
	v, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", key, err)
	}
	m[key] = v
	return json.Marshal(m)
}

// isJSONObject reports whether a reply is one JSON object, as a described
// session is; a list or anything else is left as it came.
func isJSONObject(raw []byte) bool {
	return len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '{'
}
