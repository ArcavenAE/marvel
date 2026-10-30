package daemon

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// slowPane serves the idle fixture and takes delay per capture, to stand in for
// the ~30 ms a pane read costs on a live host. It counts calls under a lock so
// the cache's reader and a query can run together.
type slowPane struct {
	mu      sync.Mutex
	delay   time.Duration
	escapes string
	calls   int
}

func (p *slowPane) PaneForeground(string) (string, int, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return "2.1.290", 120, nil
}
func (p *slowPane) CapturePane(string) (string, error) { return p.read() }
func (p *slowPane) CapturePaneEscapes(string) (string, error) {
	return p.read()
}

func (p *slowPane) read() (string, error) {
	time.Sleep(p.delay)
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return p.escapes, nil
}

func (p *slowPane) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func fleet(n int) []api.Session {
	out := make([]api.Session, n)
	for i := range out {
		s := runningSeat("claude")
		s.Name = fmt.Sprintf("s%d", i)
		s.PaneID = fmt.Sprintf("%%%d", i)
		out[i] = s
	}
	return out
}

func noClient(string) (bool, error) { return false, nil }

// The point of the cache: after a tick has read the panes, a query makes no
// tmux call and costs a fraction of the cold read. The count is the assertion;
// the time bound is loose so a slow host does not flake.
func TestWarmQueryMakesNoPaneCallAndStaysCheap(t *testing.T) {
	const n = 30
	p := &slowPane{delay: 10 * time.Millisecond, escapes: composerFixture(t, "1-empty-idle.ansi.txt")}
	var c paneCache
	now := time.Now()

	cold := fleet(n)
	start := time.Now()
	c.stamp(p, cold, noClient, now) // a first query reads afresh and wants the cache
	coldTook := time.Since(start)
	if coldTook < n*p.delay {
		t.Fatalf("cold query took %v, want at least %v: the fake is not slow, so the test shows nothing", coldTook, n*p.delay)
	}

	warm := fleet(n)
	before := p.count()
	start = time.Now()
	c.stamp(p, warm, noClient, now.Add(time.Second))
	warmTook := time.Since(start)
	if got := p.count() - before; got != 0 {
		t.Fatalf("a warm query made %d pane calls, want 0", got)
	}
	if warmTook > coldTook/5 {
		t.Fatalf("warm query took %v against %v cold; want under a fifth", warmTook, coldTook)
	}
	t.Logf("%d seats at %v per capture: cold %v, warm %v, %d pane calls warm", n, p.delay, coldTook, warmTook, p.count()-before)
	for i, s := range warm {
		if s.Composer != "empty" || s.Pane != paneIdle {
			t.Fatalf("seat %d: composer %q pane %q, want empty and idle", i, s.Composer, s.Pane)
		}
	}
}

// A reading older than the TTL is not served: the query reads that seat afresh.
func TestStaleReadingIsReadAfreshNotServed(t *testing.T) {
	p := &slowPane{escapes: composerFixture(t, "1-empty-idle.ansi.txt")}
	var c paneCache
	now := time.Now()
	c.stamp(p, fleet(1), noClient, now)
	before := p.count()
	c.stamp(p, fleet(1), noClient, now.Add(paneReadTTL-time.Second))
	if p.count() != before {
		t.Fatal("a reading inside the TTL was read again")
	}
	c.stamp(p, fleet(1), noClient, now.Add(paneReadTTL+time.Second))
	if p.count() == before {
		t.Fatal("a reading past the TTL was served without a read")
	}
}

// The tick reads only while a query asked recently, and not before the refresh
// interval has passed.
func TestTickReadsOnlyWhileWantedAndNotTooOften(t *testing.T) {
	p := &slowPane{escapes: composerFixture(t, "1-empty-idle.ansi.txt")}
	var c paneCache
	now := time.Now()
	seats := fleet(3)

	c.refresh(p, seats, noClient, now)
	if p.count() != 0 {
		t.Fatalf("a tick read %d times with nobody asking", p.count())
	}
	c.want(now)
	c.refresh(p, seats, noClient, now.Add(time.Second))
	first := p.count()
	if first == 0 {
		t.Fatal("a tick read nothing while the cache was wanted")
	}
	c.refresh(p, seats, noClient, now.Add(2*time.Second))
	if p.count() != first {
		t.Fatal("a tick inside the refresh interval read again")
	}
	c.refresh(p, seats, noClient, now.Add(paneRefreshEvery+2*time.Second))
	if p.count() == first {
		t.Fatal("a tick past the refresh interval did not read")
	}
	c.refresh(p, seats, noClient, now.Add(paneWantedFor+time.Minute))
	after := p.count()
	c.refresh(p, seats, noClient, now.Add(paneWantedFor+3*time.Minute))
	if p.count() != after {
		t.Fatal("a tick read after the wanted window ended")
	}
}

// The tick's readings are what a later query serves, and an entry for a seat
// that is gone is dropped.
func TestTickFillsTheCacheAndDropsGoneSeats(t *testing.T) {
	p := &slowPane{escapes: composerFixture(t, "1-empty-idle.ansi.txt")}
	var c paneCache
	now := time.Now()
	c.want(now)
	seats := fleet(3)
	c.refresh(p, seats, noClient, now)
	before := p.count()
	served := fleet(3)
	c.stamp(p, served, noClient, now.Add(time.Second))
	if p.count() != before {
		t.Fatal("a query after a tick read again")
	}
	if served[0].Pane != paneIdle {
		t.Fatalf("pane = %q", served[0].Pane)
	}
	c.refresh(p, seats[:1], noClient, now.Add(paneRefreshEvery+time.Second))
	if _, ok := c.get(seats[2].Key(), now.Add(paneRefreshEvery+time.Second)); ok {
		t.Fatal("an entry for a seat that is gone was kept")
	}
}

// A client-workspace seat is never captured by the tick or by a query, and the
// watchdog's stored verdict is read from the session, never from the cache.
func TestCacheKeepsTheClientGateAndTheStoredVerdict(t *testing.T) {
	p := &slowPane{escapes: composerFixture(t, "1-empty-idle.ansi.txt")}
	var c paneCache
	now := time.Now()
	c.want(now)
	client := func(string) (bool, error) { return true, nil }
	seats := fleet(2)
	c.refresh(p, seats, client, now)
	c.stamp(p, seats, client, now.Add(time.Second))
	if p.count() != 0 {
		t.Fatalf("a client-workspace seat was captured (%d calls)", p.count())
	}
	if seats[0].Pane != paneUnknown || seats[0].PaneGap != gapClient {
		t.Fatalf("pane = %q, %q", seats[0].Pane, seats[0].PaneGap)
	}

	// A word cached as idle is overridden the moment the session carries a
	// logged-out verdict, with no new read.
	var d paneCache
	d.stamp(p, fleet(1), noClient, now)
	out := fleet(1)
	out[0].HarnessState = &api.HarnessState{State: api.HarnessStateLoggedOut}
	before := p.count()
	d.stamp(p, out, noClient, now.Add(time.Second))
	if out[0].Pane != paneLoggedOut || p.count() != before {
		t.Fatalf("pane = %q after a stored logged-out verdict, calls +%d", out[0].Pane, p.count()-before)
	}
}

// The cache is read by queries while the tick writes it. Run with -race.
func TestCacheIsSafeUnderConcurrentTickAndQueries(t *testing.T) {
	p := &slowPane{escapes: composerFixture(t, "1-empty-idle.ansi.txt")}
	var c paneCache
	seats := fleet(8)
	now := time.Now()
	c.want(now)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				at := now.Add(time.Duration(i) * paneRefreshEvery)
				if g == 0 {
					c.refresh(p, seats, noClient, at)
				} else {
					c.stamp(p, fleet(8), noClient, at)
				}
			}
		}(g)
	}
	wg.Wait()
	if !strings.Contains(fmt.Sprint(c.entries[seats[0].Key()].pane), paneIdle) {
		t.Fatalf("entry = %+v", c.entries[seats[0].Key()])
	}
}

// A workspace that turns client between a tick's read and a query stops being
// served at once: the cached reading is dropped, and nothing is captured.
func TestCachedReadingIsNotServedOnceTheWorkspaceIsClient(t *testing.T) {
	p := &slowPane{escapes: composerFixture(t, "1-empty-idle.ansi.txt")}
	var c paneCache
	now := time.Now()
	c.stamp(p, fleet(1), noClient, now)
	seat := fleet(1)
	if _, ok := c.get(seat[0].Key(), now); !ok {
		t.Fatal("the entry was not cached")
	}
	before := p.count()
	c.stamp(p, seat, func(string) (bool, error) { return true, nil }, now.Add(time.Second))
	if seat[0].Composer != "unknown" || seat[0].ComposerGap != gapClient || seat[0].Pane != paneUnknown {
		t.Fatalf("composer %q (%q), pane %q after the workspace turned client", seat[0].Composer, seat[0].ComposerGap, seat[0].Pane)
	}
	if p.count() != before {
		t.Fatal("a client-workspace seat was captured")
	}
	if _, ok := c.get(seat[0].Key(), now); ok {
		t.Fatal("the cached reading of a client-workspace seat was kept")
	}
}

// The cache holds the screen reading, not the watchdog's verdict. A seat read
// while it carried a logged-out verdict shows the screen word as soon as the
// verdict is cleared, with no new read; before the verdict was kept out of the
// cached reading it kept showing logged-out for up to the TTL.
func TestClearedVerdictShowsTheScreenWordAtTheNextQuery(t *testing.T) {
	p := &slowPane{escapes: composerFixture(t, "1-empty-idle.ansi.txt")}
	var c paneCache
	now := time.Now()
	withVerdict := fleet(1)
	withVerdict[0].HarnessState = &api.HarnessState{State: api.HarnessStateLoggedOut}
	c.stamp(p, withVerdict, noClient, now)
	if withVerdict[0].Pane != paneLoggedOut {
		t.Fatalf("pane = %q with a logged-out verdict, want logged-out", withVerdict[0].Pane)
	}
	if p.count() == 0 {
		t.Fatal("the screen was not read while the verdict was set, so the cache holds nothing to serve once it clears")
	}

	cleared := fleet(1)
	before := p.count()
	c.stamp(p, cleared, noClient, now.Add(time.Second))
	if cleared[0].Pane != paneIdle {
		t.Fatalf("pane = %q one second after the verdict cleared, want idle (the screen word)", cleared[0].Pane)
	}
	if p.count() != before {
		t.Fatal("clearing the verdict caused a pane read; the cached screen reading should serve")
	}

	// And the other way: a verdict that appears shows at once over a cached word.
	set := fleet(1)
	set[0].HarnessState = &api.HarnessState{State: api.HarnessStateParked}
	c.stamp(p, set, noClient, now.Add(2*time.Second))
	if set[0].Pane != panePrompt {
		t.Fatalf("pane = %q with a parked verdict, want prompt", set[0].Pane)
	}
}
