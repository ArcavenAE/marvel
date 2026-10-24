package api

import (
	"fmt"
	"time"
)

// ManifestCadence is one [[team.role.cadence]] entry: a periodic prompt for an
// interactive seat (docs/design/declared-cadence.md V1). The durations are
// strings ("5m"), parsed when the manifest is applied.
type ManifestCadence struct {
	Name             string `toml:"name"               yaml:"name"`
	Schedule         string `toml:"schedule"           yaml:"schedule"`
	Timezone         string `toml:"timezone"           yaml:"timezone"`
	Prompt           string `toml:"prompt"             yaml:"prompt"`
	StartingDeadline string `toml:"starting_deadline"  yaml:"starting_deadline"`
	Grace            string `toml:"grace"              yaml:"grace"`
}

// Cadence is a validated cadence, carried on Role.Cadence. marvel serves the
// cadence it applied, never a file on disk.
type Cadence struct {
	Name             string        `json:"name"`
	Schedule         string        `json:"schedule"`
	Timezone         string        `json:"timezone"`
	Prompt           string        `json:"prompt"`
	StartingDeadline time.Duration `json:"starting_deadline"`
	Grace            time.Duration `json:"grace"`
}

// minCadenceDeadline is the floor for starting_deadline (V1).
const minCadenceDeadline = time.Minute

// cadenceHorizon is how far ahead the period is measured (V1).
const cadenceHorizon = 366 * 24 * time.Hour

// validateCadences checks one role's cadence entries (V1). now anchors the
// period scan.
func validateCadences(team string, r ManifestRole, now time.Time) error {
	if len(r.Cadence) == 0 {
		return nil
	}
	where := fmt.Sprintf("team %s role %s", team, r.Name)
	if r.Runtime.Mode != RuntimeModeInteractive {
		return fmt.Errorf("%s: cadence is only valid on an interactive role; a headless role takes a schedule", where)
	}
	seen := make(map[string]bool, len(r.Cadence))
	for _, c := range r.Cadence {
		if _, err := c.cadence(where, now); err != nil {
			return err
		}
		if seen[c.Name] {
			return fmt.Errorf("%s: cadence name %q is a duplicate", where, c.Name)
		}
		seen[c.Name] = true
	}
	return nil
}

// cadence validates one entry and returns it with its durations parsed.
func (c ManifestCadence) cadence(where string, now time.Time) (Cadence, error) {
	if c.Name == "" {
		return Cadence{}, fmt.Errorf("%s: cadence name is required", where)
	}
	where = fmt.Sprintf("%s cadence %q", where, c.Name)
	for _, f := range []struct{ name, value string }{
		{"schedule", c.Schedule},
		{"timezone", c.Timezone},
		{"prompt", c.Prompt},
		{"starting_deadline", c.StartingDeadline},
		{"grace", c.Grace},
	} {
		if f.value == "" {
			return Cadence{}, fmt.Errorf("%s: %s is required", where, f.name)
		}
	}
	if _, err := parseCron(c.Schedule); err != nil {
		return Cadence{}, fmt.Errorf("%s: schedule: %w", where, err)
	}
	if _, err := loadScheduleZone(c.Timezone); err != nil {
		return Cadence{}, fmt.Errorf("%s: timezone: %w", where, err)
	}
	deadline, err := scheduleDuration("starting_deadline", c.StartingDeadline)
	if err != nil {
		return Cadence{}, fmt.Errorf("%s: %w", where, err)
	}
	grace, err := scheduleDuration("grace", c.Grace)
	if err != nil {
		return Cadence{}, fmt.Errorf("%s: %w", where, err)
	}
	if deadline < minCadenceDeadline {
		return Cadence{}, fmt.Errorf("%s: starting_deadline %s must be at least %s", where, deadline, minCadenceDeadline)
	}
	if deadline > grace {
		return Cadence{}, fmt.Errorf("%s: starting_deadline %s must not exceed grace %s", where, deadline, grace)
	}
	gap, slots, err := shortestSlotGap(c.Schedule, c.Timezone, now, grace)
	if err != nil {
		return Cadence{}, fmt.Errorf("%s: %w", where, err)
	}
	if slots < 2 {
		return Cadence{}, fmt.Errorf("%s: schedule %q has fewer than two slots in the next 366 days, so it has no period to hold grace under", where, c.Schedule)
	}
	if gap <= grace {
		return Cadence{}, fmt.Errorf("%s: grace %s must be less than the period; two slots are %s apart", where, grace, gap)
	}
	return Cadence{
		Name: c.Name, Schedule: c.Schedule, Timezone: c.Timezone, Prompt: c.Prompt,
		StartingDeadline: deadline, Grace: grace,
	}, nil
}

// shortestSlotGap walks the schedule's slots over the next 366 days and
// returns the shortest gap between two consecutive ones, and how many slots
// it saw. It stops at the first gap no longer than floor, because the caller
// refuses on that gap whatever a later one is; that keeps a per-minute
// schedule from being walked for a year before it is refused.
func shortestSlotGap(schedule, timezone string, now time.Time, floor time.Duration) (gap time.Duration, slots int, err error) {
	p := SchedulePolicy{Cron: schedule, Timezone: timezone}
	end := now.Add(cadenceHorizon)
	var prev time.Time
	at := now
	for {
		next, err := p.NextFiring(at)
		if err != nil {
			return 0, slots, err
		}
		if next.After(end) {
			return gap, slots, nil
		}
		slots++
		if slots > 1 {
			if g := next.Sub(prev); gap == 0 || g < gap {
				gap = g
				if gap <= floor {
					return gap, slots, nil
				}
			}
		}
		prev, at = next, next
	}
}
