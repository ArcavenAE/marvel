package runtime

import (
	"fmt"
	"strings"
)

// Generic is the fallback adapter for any CLI that accepts a prompt on
// stdin. Minimal integration: environment-based identity only. Marvel
// observes the session with a capture-pane history scrape — see
// GenericScraper.
type Generic struct{}

// PaneRangeCapturer is the slice of the tmux driver GenericScraper needs.
// Declaring it here keeps package runtime free of a hard tmux dependency
// and lets tests drive the scraper without a tmux server.
type PaneRangeCapturer interface {
	CapturePaneRange(paneID string, start, end int) (string, error)
}

const (
	// scrapeHistoryStart reaches as far into scrollback as the pane
	// retains. tmux clamps an over-large negative -S to the oldest
	// retained line, so the effective reach is min(this, history-limit).
	scrapeHistoryStart = -100000
	// scrapeVisibleEnd reaches the bottom of the visible region. tmux
	// clamps an over-large -E to the last row of the pane.
	scrapeVisibleEnd = 100000
)

// GenericScraper is the observation path for a generic session's pane: a
// capture-pane history scrape with a per-session high-water mark. It reads
// scrollback with CapturePaneRange (the "capture-pane -S <start> -E <end>"
// history form), not Driver.CapturePane (the "capture-pane -p" visible
// region that finding-005 measured at 0.48% coverage on a burst). The
// visible form caps at rows*poll_hz lines and drops everything that
// scrolls off between polls; the history form retains it.
//
// Each Scrape returns only lines past the high-water mark and advances the
// mark, so the consumer never sees a line twice. One GenericScraper drives
// one pane and is not safe for concurrent use; the observation loop that
// owns a session steps it from a single goroutine.
//
// Coverage still depends on the session's tmux history-limit. At the 2000
// line default a burst larger than the scrollback is lost before a poll
// can read it (finding-005 case (f): 20% of a 20000-line burst). Raising
// the limit at session creation is tracked separately as aae-orc-22sz; this
// scraper is correct within whatever limit is in force.
type GenericScraper struct {
	cap    PaneRangeCapturer
	paneID string
	// delivered is the high-water mark: the count of pane lines already
	// returned to the consumer. It advances each poll and never re-emits.
	delivered int
}

// NewGenericScraper builds a scraper for one pane.
func NewGenericScraper(capturer PaneRangeCapturer, paneID string) *GenericScraper {
	return &GenericScraper{cap: capturer, paneID: paneID}
}

// Scrape captures the pane's retained scrollback and returns the lines
// produced since the previous call, advancing the high-water mark. A poll
// that finds no new content returns an empty slice and no error.
func (s *GenericScraper) Scrape() ([]string, error) {
	content, err := s.cap.CapturePaneRange(s.paneID, scrapeHistoryStart, scrapeVisibleEnd)
	if err != nil {
		return nil, fmt.Errorf("scrape pane %s: %w", s.paneID, err)
	}

	lines := strings.Split(content, "\n")
	// Drop trailing blank lines: the final-newline empty element plus any
	// empty visible rows tmux padded the capture with. Counting them would
	// desync the mark once real output fills those rows.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) < s.delivered {
		// The buffer shrank (screen clear, alternate-screen switch).
		// Rebaseline to the current extent rather than re-emit history;
		// the scrape is best-effort for harnesses that repaint.
		s.delivered = len(lines)
		return nil, nil
	}
	if len(lines) == s.delivered {
		return nil, nil
	}

	fresh := append([]string(nil), lines[s.delivered:]...)
	s.delivered = len(lines)
	return fresh, nil
}

func (g *Generic) Name() string { return "generic" }

func (g *Generic) Prepare(ctx *LaunchContext) (*LaunchResult, error) {
	binary := resolveCommand(&ctx.Session.Runtime)
	if binary == "" {
		return nil, ErrNoCommand
	}

	args := make([]string, len(ctx.Session.Runtime.Args))
	copy(args, ctx.Session.Runtime.Args)

	env := baseEnv(ctx)

	return &LaunchResult{
		Command: buildCommand(binary, args),
		Env:     env,
	}, nil
}

func init() {
	var _ Adapter = (*Generic)(nil)
}
