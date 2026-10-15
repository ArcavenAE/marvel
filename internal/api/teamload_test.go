package api

import (
	"reflect"
	"testing"
	"time"
)

func loadSeat(name, role string, state SessionState, ticks ActiveTicks) Session {
	return Session{Name: name, Workspace: "ws", Team: "squad", Role: role, State: state, ActiveTicks: ticks}
}

func readTicks(active, total int) ActiveTicks {
	return ActiveTicks{Active: active, Total: total, Observable: true, Full: true, At: time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)}
}

var loadNow = time.Date(2026, 10, 15, 1, 0, 2, 0, time.UTC)

func loadTeam() Team {
	return Team{Name: "squad", Workspace: "ws", Roles: []Role{{Name: "lead", Replicas: 1}, {Name: "worker", Replicas: 3}}}
}

// Seats declared is the replica sum, seats running counts running sessions of
// this team only, and the agent names are the running seats, sorted.
func TestRollTeamLoadCountsSeatsAndNames(t *testing.T) {
	other := loadSeat("other-worker-g1-0", "worker", SessionRunning, readTicks(1, 2))
	other.Team = "other"
	sessions := []Session{
		loadSeat("squad-worker-g1-1", "worker", SessionRunning, ActiveTicks{}),
		loadSeat("squad-lead-g1-0", "lead", SessionRunning, ActiveTicks{}),
		loadSeat("squad-worker-g1-2", "worker", SessionCrashed, ActiveTicks{}),
		other,
	}
	got := RollTeamLoad(loadTeam(), sessions, loadNow)
	if got.Declared != 4 || got.Running != 2 {
		t.Errorf("declared %d running %d, want 4 and 2", got.Declared, got.Running)
	}
	want := []string{"squad-lead-g1-0", "squad-worker-g1-1"}
	if !reflect.DeepEqual(got.Agents, want) {
		t.Errorf("agents = %v, want %v", got.Agents, want)
	}
	if got.Workspace != "ws" || got.Team != "squad" {
		t.Errorf("identity = %s/%s", got.Workspace, got.Team)
	}
}

// The mean is over the seats that have a reading. A seat with no activity
// channel, a ring that has not watched the whole window, or no ticks is left
// out of both the mean and the measured count.
func TestRollTeamLoadMeansOnlyMeasuredSeats(t *testing.T) {
	notObservable := readTicks(9, 10)
	notObservable.Observable = false
	notFull := readTicks(9, 10)
	notFull.Full = false
	sessions := []Session{
		loadSeat("squad-worker-g1-0", "worker", SessionRunning, readTicks(50, 100)),
		loadSeat("squad-worker-g1-1", "worker", SessionRunning, readTicks(100, 100)),
		loadSeat("squad-worker-g1-2", "worker", SessionRunning, notObservable),
		loadSeat("squad-lead-g1-0", "lead", SessionRunning, notFull),
	}
	got := RollTeamLoad(loadTeam(), sessions, loadNow)
	if got.ActiveMeasured != 2 || got.ActiveMeanPct != 75 {
		t.Errorf("mean %v over %d seats, want 75 over 2", got.ActiveMeanPct, got.ActiveMeasured)
	}
	if got.Running != 4 {
		t.Errorf("running = %d, want the four running seats counted whatever they report", got.Running)
	}
}

// A seat that reads 0% is a measurement and counts; no measured seat leaves the
// count at zero so a reader prints a dash, not 0%.
func TestRollTeamLoadSeparatesZeroFromNoReading(t *testing.T) {
	zero := RollTeamLoad(loadTeam(), []Session{loadSeat("squad-lead-g1-0", "lead", SessionRunning, readTicks(0, 100))}, loadNow)
	if zero.ActiveMeasured != 1 || zero.ActiveMeanPct != 0 {
		t.Errorf("a measured 0%% seat: mean %v over %d, want 0 over 1", zero.ActiveMeanPct, zero.ActiveMeasured)
	}
	none := RollTeamLoad(loadTeam(), []Session{loadSeat("squad-lead-g1-0", "lead", SessionRunning, ActiveTicks{})}, loadNow)
	if none.ActiveMeasured != 0 {
		t.Errorf("an unmeasured seat counted: measured = %d", none.ActiveMeasured)
	}
}

// Only a running seat is limited; the row says when it was computed and when it
// goes stale.
func TestRollTeamLoadCountsLimitedAndStampsValidity(t *testing.T) {
	limited := loadSeat("squad-lead-g1-0", "lead", SessionRunning, ActiveTicks{})
	limited.Condition = ConditionLimited
	stoppedLimited := loadSeat("squad-worker-g1-0", "worker", SessionCrashed, ActiveTicks{})
	stoppedLimited.Condition = ConditionLimited
	got := RollTeamLoad(loadTeam(), []Session{limited, stoppedLimited}, loadNow)
	if got.Limited != 1 {
		t.Errorf("limited = %d, want 1", got.Limited)
	}
	if !got.ObservedAt.Equal(loadNow) || !got.ValidUntil.Equal(loadNow.Add(TeamLoadValidity)) {
		t.Errorf("observed %v valid until %v, want %v and +%v", got.ObservedAt, got.ValidUntil, loadNow, TeamLoadValidity)
	}
}
