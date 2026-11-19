package workload

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// The restart wait and a Stop can both be ready when the loop comes back to its
// select: the loop is inside the driver's Exited call for the crash, the wait's
// channel already holds its value, and a Stop is blocked sending its request.
// Go picks one ready case at random, so the test runs the race many times. A
// Stop that is queued when the wait ends must win (the ruling on aae-orc-f5yoz):
//
//   - after Stop returns, no child is running;
//   - nothing is spawned: one start, and the crash is the only exit.
//
// It logs how often each case won.
func TestAQueuedStopWinsOverTheRestartWait(t *testing.T) {
	const trials = 30
	var stopWon, restartWon int
	for i := 0; i < trials; i++ {
		spec := procSpec(t)
		p, d, c := newProc(t, spec)

		// On the first Exited, the crash, the loop is held inside the call:
		// the wait's channel is made ready first, then a Stop is queued and
		// given time to block on the request channel. When the call returns the
		// loop's next select sees both.
		var held atomic.Bool
		stopped := make(chan struct{})
		d.hook = func(where string) {
			if where != "Exited" || !held.CompareAndSwap(false, true) {
				return
			}
			c.fire <- c.at
			go func() {
				p.Stop(false)
				close(stopped)
			}()
			time.Sleep(100 * time.Millisecond)
		}

		if err := p.Spawn(context.Background()); err != nil {
			t.Fatal(err)
		}
		first := p.Snapshot().Child
		killPid(t, first.Pid())
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Fatalf("trial %d: Stop did not return", i)
		}

		if p.prober.Same(first) {
			t.Errorf("trial %d: the first child is still running after Stop", i)
		}
		st := p.Snapshot()
		if st.Ready || st.Pid != 0 {
			t.Errorf("trial %d: snapshot after Stop = %+v, want no child", i, st)
		}
		starts, exits := d.startsCopy(), d.exitsCopy()
		switch len(starts) {
		case 1:
			stopWon++
			if len(exits) != 1 || exits[0].Requested {
				t.Errorf("trial %d: Stop won but exits = %+v, want only the crash", i, exits)
			}
		case 2:
			restartWon++
			t.Errorf("trial %d: a child was spawned although a Stop was queued (starts = %+v, exits = %+v)", i, starts[1], exits)
		default:
			t.Errorf("trial %d: %d starts, want 1 or 2", i, len(starts))
		}
	}
	t.Logf("of %d trials the Stop won %d and the restart wait won %d", trials, stopWon, restartWon)
}

// holdAtTheCrash runs one crash of a fresh Process with the restart wait ready
// and queued() running in a goroutine, blocked on the request channel, when the
// loop comes back to its select. It returns what queued() returned, once the
// loop has served it.
func holdAtTheCrash(t *testing.T, queued func(p *Process) error) (*Process, *recDriver, error) {
	t.Helper()
	p, d, c := newProc(t, procSpec(t))
	var held atomic.Bool
	errc := make(chan error, 1)
	d.hook = func(where string) {
		if where != "Exited" || !held.CompareAndSwap(false, true) {
			return
		}
		c.fire <- c.at
		go func() { errc <- queued(p) }()
		time.Sleep(100 * time.Millisecond)
	}
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	killPid(t, p.Snapshot().Child.Pid())
	select {
	case err := <-errc:
		return p, d, err
	case <-time.After(10 * time.Second):
		t.Fatal("the queued request was not served")
		return nil, nil, nil
	}
}

// A Spawn queued when the wait ends is the start, as it is when it arrives
// during the wait: it succeeds and the wait's own respawn does not also run.
func TestASpawnQueuedWhenTheRestartWaitEndsIsTheStart(t *testing.T) {
	for i := 0; i < 20; i++ {
		p, d, err := holdAtTheCrash(t, func(p *Process) error { return p.Spawn(context.Background()) })
		if err != nil {
			t.Errorf("trial %d: Spawn = %v, want nil", i, err)
		}
		starts := d.startsCopy()
		if len(starts) != 2 || starts[1].How != StartSpawned {
			t.Errorf("trial %d: starts = %+v, want the first start and one Spawn", i, starts)
		}
		if st := p.Snapshot(); !st.Ready || st.Pid == 0 {
			t.Errorf("trial %d: snapshot = %+v, want the new child running", i, st)
		}
		p.Stop(false)
	}
}

// A Restart queued when the wait ends is the start and carries its reason: the
// wait's own respawn does not also run, so there is no third start.
func TestARestartQueuedWhenTheRestartWaitEndsIsTheStart(t *testing.T) {
	for i := 0; i < 20; i++ {
		p, d, err := holdAtTheCrash(t, func(p *Process) error { return p.Restart("asked") })
		if err != nil {
			t.Errorf("trial %d: Restart = %v, want nil", i, err)
		}
		starts := d.startsCopy()
		if len(starts) != 2 || starts[1].How != StartRestarted || starts[1].Reason != "asked" {
			t.Errorf("trial %d: starts = %+v, want the first start and one restart with its reason", i, starts)
		}
		if st := p.Snapshot(); !st.Ready || st.Pid == 0 {
			t.Errorf("trial %d: snapshot = %+v, want the new child running", i, st)
		}
		p.Stop(false)
	}
}
