package daemon

import (
	"fmt"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// I-477 slice 2, the reader half: the transcript reader keeps fresh input, cache
// reads, cache writes and the prompt sum beside the output sum, once per message
// id at the largest figure seen for it, with TIN + CR + CW = PROMPT.

// msgIO is one assistant record of message id with all four usage classes.
func msgIO(id string, in, cacheRead, cacheWrite, out int) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":"u-%s","message":{"id":%q,"role":"assistant","model":"claude-x","usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d},"content":[{"type":"text","text":"x"}]}}`, id, id, in, out, cacheWrite, cacheRead)
}

func (r *usageRig) wantSplit(fresh, cacheRead, cacheWrite, prompt int) {
	r.t.Helper()
	s := r.session()
	for _, c := range []struct {
		name string
		got  *int
		want int
	}{
		{"SpendFresh", s.SpendFresh, fresh},
		{"SpendCacheRead", s.SpendCacheRead, cacheRead},
		{"SpendCacheWrite", s.SpendCacheWrite, cacheWrite},
		{"SpendPromptTokens", s.SpendPromptTokens, prompt},
	} {
		if c.got == nil {
			r.t.Errorf("%s is nil, want %d", c.name, c.want)
		} else if *c.got != c.want {
			r.t.Errorf("%s = %d, want %d", c.name, *c.got, c.want)
		}
	}
	if s.SpendFresh != nil && s.SpendCacheRead != nil && s.SpendCacheWrite != nil && s.SpendPromptTokens != nil {
		if sum := *s.SpendFresh + *s.SpendCacheRead + *s.SpendCacheWrite; sum != *s.SpendPromptTokens {
			r.t.Errorf("TIN + CR + CW = %d, PROMPT = %d, want them equal", sum, *s.SpendPromptTokens)
		}
	}
}

// A claude seat's transcript carries the classes beside each other, so the
// prompt of a message is their sum. A record repeated under one id counts once,
// and a later larger figure adds only the growth.
func TestTranscriptTinPlusCacheEqualsPrompt(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once() // first sight: no file yet
	r.lines(msgIO("m1", 10, 400, 100, 50), msgIO("m1", 10, 400, 100, 50), msgIO("m2", 20, 900, 0, 70))
	r.u.Once()
	r.wantSplit(30, 1300, 100, 1430)
	r.lines(msgIO("m2", 25, 900, 0, 80))
	r.u.Once()
	r.wantSplit(35, 1300, 100, 1435)
}

// A record with no cache classes is a metered zero for them, not an absent one.
func TestTranscriptRecordWithoutCacheClassesCountsZero(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	r.lines(`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":12,"output_tokens":5}}}`)
	r.u.Once()
	r.wantSplit(12, 0, 0, 12)
}

// The seat is the reader's, and stays so, once the reader has written its prompt
// sum and its place. The old marker (a non-nil SpendPromptTokens) would hand the
// seat to the accountant after the first pass and stop the reading.
func TestReaderKeepsReadingAfterItWritesThePromptSum(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	r.lines(msgIO("m1", 10, 100, 0, 5))
	r.u.Once()
	r.lines(msgIO("m2", 20, 200, 0, 7))
	r.u.Once()
	r.want(12, false)
	r.wantSplit(30, 300, 0, 330)
}

// A seat the accountant metered (a prompt sum and no place) is still left alone.
func TestAccountantOwnedSeatHasAPromptSumAndNoTail(t *testing.T) {
	r := newUsageRig(t)
	out, prompt, fresh := 999, 5000, 5000
	r.store.UpdateSessionSpend(r.key, api.SessionSpend{Out: &out, PromptTokens: &prompt, Fresh: &fresh})
	r.u.Once()
	r.lines(msgIO("m1", 10, 100, 0, 5))
	r.u.Once()
	s := r.session()
	if s.SpendPromptTokens == nil || *s.SpendPromptTokens != 5000 || s.SpendOut == nil || *s.SpendOut != 999 {
		t.Errorf("an accountant-owned seat has prompt %v out %v, want 5000 and 999", s.SpendPromptTokens, s.SpendOut)
	}
	if s.SpendTail != nil {
		t.Errorf("the reader wrote a place %+v into an accountant-owned seat", *s.SpendTail)
	}
}
