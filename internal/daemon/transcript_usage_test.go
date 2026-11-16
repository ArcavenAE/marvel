package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The transcript reader of I-477 slice 1. Every fixture is a synthetic file with
// typed fields only; no real seat's transcript is read.

const usageSession = "99999999-2222-3333-4444-555555555555"

type usageRig struct {
	t     *testing.T
	store *api.Store
	u     *txUsage
	cfg   string
	key   string
	reads int
}

func newUsageRig(t *testing.T) *usageRig {
	t.Helper()
	r := &usageRig{t: t, store: api.NewStore(), cfg: t.TempDir()}
	r.u = newTxUsage(r.store, func(string) (bool, error) { return false, nil })
	inner := r.u.tailRead
	r.u.tailRead = func(path string, from int64) ([]byte, error) {
		r.reads++
		return inner(path, from)
	}
	s := &api.Session{
		Name: "seat", Workspace: "ws", Team: "t", Role: "r",
		Runtime: api.Runtime{Name: "claude", Command: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": r.cfg}},
		State:   api.SessionRunning, PaneID: "%1", HarnessSessionID: usageSession,
	}
	if err := r.store.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	r.key = s.Key()
	return r
}

func (r *usageRig) path() string {
	return filepath.Join(r.cfg, "projects", "proj", usageSession+".jsonl")
}

// append adds raw text to the transcript, creating it on first use.
func (r *usageRig) append(raw string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(r.path()), 0o700); err != nil {
		r.t.Fatal(err)
	}
	f, err := os.OpenFile(r.path(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(raw); err != nil {
		r.t.Fatal(err)
	}
}

func (r *usageRig) lines(lines ...string) {
	r.t.Helper()
	for _, l := range lines {
		r.append(l + "\n")
	}
}

// replace rewrites the file whole, as a rotation or a truncation does.
func (r *usageRig) replace(lines ...string) {
	r.t.Helper()
	tmp := r.path() + ".new"
	var body string
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
	// The new file exists beside the old one, so the two cannot share an inode.
	if err := os.Rename(tmp, r.path()); err != nil {
		r.t.Fatal(err)
	}
}

func (r *usageRig) session() api.Session {
	r.t.Helper()
	s, err := r.store.GetSession(r.key)
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

// want fails unless the seat's output sum is out, with the given partial flag.
func (r *usageRig) want(out int, partial bool) {
	r.t.Helper()
	s := r.session()
	if s.SpendOut == nil {
		r.t.Fatalf("SpendOut is nil, want %d", out)
	}
	if *s.SpendOut != out {
		r.t.Errorf("SpendOut = %d, want %d", *s.SpendOut, out)
	}
	if s.SpendPartial != partial {
		r.t.Errorf("SpendPartial = %v, want %v", s.SpendPartial, partial)
	}
}

// msg is one assistant record of message id carrying output tokens.
func msg(id string, out int) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":"u-%s","message":{"id":%q,"role":"assistant","model":"claude-x","usage":{"input_tokens":3,"output_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"x"}]}}`, id, id, out)
}

// A file that appears after the seat was first seen is read from byte 0, so its
// sum is exact; a record repeated under one message id counts once.
func TestTranscriptUsageCountedOncePerMessageId(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once() // first sight: no file yet
	r.lines(msg("m1", 120), msg("m1", 120), msg("m2", 80))
	r.u.Once()
	r.want(200, false)
	r.lines(msg("m2", 80), msg("m3", 5))
	r.u.Once()
	r.want(205, false)
}

// A shrunk or replaced file resets the offset, never the total, and marks the
// sum partial; ids already counted are not counted again.
func TestTranscriptShrinkRescansNotNegative(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	r.lines(msg("m1", 120), msg("m2", 80), msg("pad", 1), msg("pad2", 1))
	r.u.Once()
	r.want(202, false)
	r.replace(msg("m2", 80), msg("m3", 50))
	r.u.Once()
	s := r.session()
	if s.SpendOut == nil || *s.SpendOut < 202 {
		t.Fatalf("SpendOut = %v after a shrink, want at least 202: a shrink never lowers the total", s.SpendOut)
	}
	r.want(252, true)
}

// A line still being written is read again once it is finished.
func TestTranscriptPartialLastLineNotCounted(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	r.lines(msg("m1", 10))
	r.append(msg("m2", 300)) // no newline yet
	r.u.Once()
	r.want(10, false)
	r.append("\n")
	r.u.Once()
	r.want(310, false)
}

// A seat with no transcript has no token figure: nil, never zero.
func TestNoTranscriptLeavesTokensNil(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	s := r.session()
	if s.SpendOut != nil || s.SpendPartial {
		t.Errorf("a seat with no transcript has SpendOut %v, SpendPartial %v, want nil and false", s.SpendOut, s.SpendPartial)
	}
	if err := r.store.UpdateSession(r.key, func(s *api.Session) error { s.HarnessSessionID = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	r.u.Once()
	if got := r.session().SpendOut; got != nil {
		t.Errorf("a seat with no session id has SpendOut %d, want nil", *got)
	}
}

// First sight of a file that already has history starts at its end: what came
// before is not counted, and the sum says so.
func TestSumFromMidSessionBaselineIsPartial(t *testing.T) {
	r := newUsageRig(t)
	r.lines(msg("old1", 5000), msg("old2", 7000))
	r.u.Once()
	r.want(0, true)
	r.lines(msg("m3", 40))
	r.u.Once()
	r.want(40, true)
}

// A file that is still empty at first sight loses nothing, so its sum is exact.
func TestEmptyTranscriptAtFirstSightIsExact(t *testing.T) {
	r := newUsageRig(t)
	r.append("")
	r.u.Once()
	r.lines(msg("m1", 9))
	r.u.Once()
	r.want(9, false)
}

// An unchanged file is not read again.
func TestUnchangedTranscriptIsNotReadAgain(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	r.lines(msg("m1", 9))
	r.u.Once()
	before := r.reads
	r.u.Once()
	r.u.Once()
	if r.reads != before {
		t.Errorf("an unchanged transcript was read %d more times", r.reads-before)
	}
}

// Until the operator rules on client workspaces, a client seat's transcript is
// not read, and neither is one whose workspace cannot be told.
func TestClientWorkspaceTranscriptNotRead(t *testing.T) {
	for name, client := range map[string]func(string) (bool, error){
		"client":  func(string) (bool, error) { return true, nil },
		"unknown": func(string) (bool, error) { return false, fmt.Errorf("no such workspace") },
		"nil":     nil,
	} {
		r := newUsageRig(t)
		r.u.client = client
		r.u.Once()
		r.lines(msg("m1", 120))
		r.u.Once()
		if r.reads != 0 {
			t.Errorf("%s: the transcript was opened %d times", name, r.reads)
		}
		if got := r.session().SpendOut; got != nil {
			t.Errorf("%s: SpendOut = %d, want nil", name, *got)
		}
	}
}

// A seat the usage accountant meters keeps the accountant's figures.
func TestAccountantMeteredSeatKeepsItsSpend(t *testing.T) {
	r := newUsageRig(t)
	out, prompt := 999, 5000
	r.store.UpdateSessionSpend(r.key, api.SessionSpend{Out: &out, PromptTokens: &prompt})
	r.u.Once()
	r.lines(msg("m1", 120))
	r.u.Once()
	s := r.session()
	if s.SpendOut == nil || *s.SpendOut != 999 || s.SpendPartial {
		t.Errorf("a metered seat has SpendOut %v, SpendPartial %v, want 999 and false", s.SpendOut, s.SpendPartial)
	}
}

// An id repeated after the file was replaced counts once, and a changed inode
// with the same size is a replacement too.
func TestTranscriptInodeChangeKeepsTheTotal(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	r.lines(msg("m1", 100))
	r.u.Once()
	r.want(100, false)
	r.replace(msg("m1", 100)) // same size, new inode
	r.u.Once()
	r.want(100, true)
}

// A file truncated in place keeps its inode and shrinks: the offset resets, the
// total and the seen ids stay, and the sum is partial.
func TestTranscriptInPlaceTruncationResetsTheOffset(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	r.lines(msg("m1", 100), msg("m2", 60), msg("pad", 1), msg("pad2", 1))
	r.u.Once()
	r.want(162, false)
	if err := os.Truncate(r.path(), 10); err != nil {
		t.Fatal(err)
	}
	r.lines(msg("m9", 40))
	r.u.Once()
	s := r.session()
	if s.SpendOut == nil || *s.SpendOut < 162 {
		t.Fatalf("SpendOut = %v after an in-place truncation, want at least 162", s.SpendOut)
	}
	r.want(202, true)
}

// Lines the reader cannot count are skipped, not fatal and not counted: text
// that is not JSON, user records, an assistant record with no message id or no
// usage, and an output count that is not a number.
func TestTranscriptIgnoresRecordsItCannotCount(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	r.lines(
		"not json at all",
		`{"type":"user","uuid":"u","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","usage":{"output_tokens":500}}}`,
		`{"type":"assistant","message":{"id":"noUsage","role":"assistant","content":[]}}`,
		`{"type":"assistant","message":{"id":"badNum","role":"assistant","usage":{"output_tokens":"many"}}}`,
		`{"type":"assistant","message":{"id":"neg","role":"assistant","usage":{"output_tokens":-7}}}`,
		msg("ok", 12),
	)
	r.u.Once()
	r.want(12, false)
}

// Records of one message id can carry a growing output count as the reply is
// streamed. The id counts once, at the largest figure seen, and a smaller later
// figure never lowers it.
func TestTranscriptLaterLargerUsageForOneIdCountsTheDifference(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	r.lines(msg("m1", 10))
	r.u.Once()
	r.want(10, false)
	r.lines(msg("m1", 25))
	r.u.Once()
	r.want(25, false)
	r.lines(msg("m1", 5), msg("m1", 25))
	r.u.Once()
	r.want(25, false)
}

// Within one daemon life a partial sum stays partial, however much exact
// output follows: the earlier output was never counted.
func TestPartialFlagDoesNotClearWithinADaemonLife(t *testing.T) {
	r := newUsageRig(t)
	r.lines(msg("old", 900))
	r.u.Once()
	r.want(0, true)
	for i := range 5 {
		r.lines(msg(fmt.Sprintf("n%d", i), 10))
		r.u.Once()
	}
	r.want(50, true)
}

// The reader owns output tokens only. It leaves the prompt sum alone.
func TestReaderLeavesPromptTokensAlone(t *testing.T) {
	r := newUsageRig(t)
	r.u.Once()
	r.lines(msg("m1", 20))
	r.u.Once()
	r.want(20, false)
	if got := r.session().SpendPromptTokens; got != nil {
		t.Errorf("SpendPromptTokens = %d, want nil: the reader does not write prompt tokens", *got)
	}
}

// Only a running claude seat is read.
func TestReaderReadsOnlyRunningClaudeSeats(t *testing.T) {
	for name, mutate := range map[string]func(*api.Session){
		"codex runtime": func(s *api.Session) { s.Runtime.Name = "codex" },
		"succeeded":     func(s *api.Session) { s.State = api.SessionSucceeded },
		"failed":        func(s *api.Session) { s.State = api.SessionFailed },
	} {
		r := newUsageRig(t)
		if err := r.store.UpdateSession(r.key, func(s *api.Session) error { mutate(s); return nil }); err != nil {
			t.Fatal(err)
		}
		r.u.Once()
		r.lines(msg("m1", 20))
		r.u.Once()
		if r.reads != 0 {
			t.Errorf("%s: the transcript was opened %d times", name, r.reads)
		}
		if got := r.session().SpendOut; got != nil {
			t.Errorf("%s: SpendOut = %d, want nil", name, *got)
		}
	}
}
