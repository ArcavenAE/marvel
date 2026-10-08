package api

import (
	"fmt"
	"math/bits"
	"sort"
	"strconv"
	"strings"
	"time"
)

// cronSpec is a compiled five-field cron: one bit per allowed value.
type cronSpec struct {
	minute, hour, dom, month, dow uint64
	// domStar and dowStar record a day field that starts with "*". Vixie's
	// rule: when both day fields are restricted, a day matching either one
	// fires; otherwise both must match (and a "*" field matches every day).
	domStar, dowStar bool
	// wildcard is Vixie's (cronie's) test for an interval job: the minute
	// or hour field starts with "*". Such a job runs on real elapsed time
	// through an offset change; any other job is fixed-time.
	wildcard bool
}

func compileCron(expr string) (cronSpec, error) {
	fields, err := parseCron(expr)
	if err != nil {
		return cronSpec{}, err
	}
	var bits [5]uint64
	for i, f := range fields {
		spec := cronFields[i]
		for _, item := range strings.Split(f, ",") {
			bits[i] |= expandCronItem(item, spec.min, spec.max)
		}
	}
	// Day-of-week 7 is Sunday, the same day as 0.
	if bits[4]&(1<<7) != 0 {
		bits[4] |= 1
	}
	return cronSpec{
		minute: bits[0], hour: bits[1], dom: bits[2], month: bits[3], dow: bits[4],
		domStar:  strings.HasPrefix(fields[2], "*"),
		dowStar:  strings.HasPrefix(fields[4], "*"),
		wildcard: strings.HasPrefix(fields[0], "*") || strings.HasPrefix(fields[1], "*"),
	}, nil
}

// expandCronItem turns one already-validated item into its bits.
func expandCronItem(item string, lo, hi int) uint64 {
	base, stepStr, _ := strings.Cut(item, "/")
	step := 1
	if stepStr != "" {
		step, _ = strconv.Atoi(stepStr)
	}
	from, to := lo, hi
	if base != "*" {
		a, b, isRange := strings.Cut(base, "-")
		from, _ = strconv.Atoi(a)
		to = from
		if isRange {
			to, _ = strconv.Atoi(b)
		}
	}
	var bits uint64
	for v := from; v <= to; v += step {
		bits |= 1 << v
	}
	return bits
}

func (c cronSpec) matchesDay(d time.Time) bool {
	if c.month&(1<<int(d.Month())) == 0 {
		return false
	}
	domOK := c.dom&(1<<d.Day()) != 0
	dowOK := c.dow&(1<<int(d.Weekday())) != 0
	if c.domStar || c.dowStar {
		return domOK && dowOK
	}
	return domOK || dowOK
}

// maxScheduleScanDays bounds the search for the next firing. A cron
// allowed by parse fires at least once in eight years (29 February, past
// a skipped leap year such as 2100), so nine is a ceiling, not a guess.
const maxScheduleScanDays = 9 * 366

// NextFiring returns the first firing strictly after after, in the
// schedule's zone, with the daylight-saving edges fixed rather than left
// to a library default (scheduled-runs section 2a):
//
//   - A firing time inside the spring-forward gap does not exist. It fires
//     once, at the first instant after the gap, and every gap firing
//     coalesces with a genuine firing at that instant into one.
//   - A firing time inside the fall-back overlap happens twice. A
//     fixed-time job fires once, at the first occurrence. A wildcard job
//     (minute or hour field starting with "*") runs on real elapsed time,
//     so it fires in both occurrences: an hourly job does not lose an hour.
func (p SchedulePolicy) NextFiring(after time.Time) (time.Time, error) {
	loc, err := loadScheduleZone(p.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	spec, err := compileCron(p.Cron)
	if err != nil {
		return time.Time{}, err
	}
	local := after.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day()-1, 0, 0, 0, 0, time.UTC)
	var best time.Time
	for i := 0; i < maxScheduleScanDays; i++ {
		for _, at := range spec.firingsOn(day.AddDate(0, 0, i), loc) {
			if at.After(after) && (best.IsZero() || at.Before(best)) {
				best = at
			}
		}
		// A day's firings can sit a few hours either side of its own
		// midnight, so check one day past the first day that had one.
		if !best.IsZero() && i > 0 {
			for _, at := range spec.firingsOn(day.AddDate(0, 0, i+1), loc) {
				if at.After(after) && at.Before(best) {
					best = at
				}
			}
			return best, nil
		}
	}
	return time.Time{}, fmt.Errorf("cron %q in %s has no firing within %d days", p.Cron, p.Timezone, maxScheduleScanDays)
}

// CatchUp is the recovery walk: given the due time the clock was waiting
// for (after) and the present (now), it returns the newest due time in
// (after, now], or after when there is none, and how many due times fell in
// that span. The result is the same as stepping NextFiring forward from
// after, but the cost is one step per calendar day, not one per due time.
// A day with no offset change counts its firings from the cron fields
// alone. Only the first and last days, and a day whose offset changes, are
// expanded minute by minute. So a minutely schedule down for a year costs
// about four expanded days, a few milliseconds, where the forward walk
// takes minutes, and the clock runs this under the store lock.
func (p SchedulePolicy) CatchUp(after, now time.Time) (newest time.Time, missed int, err error) {
	if !now.After(after) {
		return after, 0, nil
	}
	loc, err := loadScheduleZone(p.Timezone)
	if err != nil {
		return time.Time{}, 0, err
	}
	spec, err := compileCron(p.Cron)
	if err != nil {
		return time.Time{}, 0, err
	}
	perDay := bits.OnesCount64(spec.hour) * bits.OnesCount64(spec.minute)
	first, last := localDate(after.In(loc)), localDate(now.In(loc))
	inSpan := func(at time.Time) bool { return at.After(after) && !at.After(now) }

	newest = after
	var lastDay time.Time
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		if !spec.matchesDay(d) {
			continue
		}
		if !d.Equal(first) && !d.Equal(last) && !offsetChangesOn(d, loc) {
			missed += perDay
			lastDay = d
			continue
		}
		for _, at := range spec.firingsOn(d, loc) {
			if inSpan(at) {
				missed++
				lastDay = d
			}
		}
	}
	if !lastDay.IsZero() {
		for _, at := range spec.firingsOn(lastDay, loc) {
			if inSpan(at) && at.After(newest) {
				newest = at
			}
		}
	}
	return newest, missed, nil
}

// localDate is the calendar date of a local time, as UTC midnight, the form
// firingsOn and matchesDay take.
func localDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// offsetChangesOn reports whether loc's UTC offset changes during the local
// calendar day d (given as UTC midnight). It samples the day's start, noon
// and the next day's start, which catches every real transition.
func offsetChangesOn(d time.Time, loc *time.Location) bool {
	_, start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc).Zone()
	_, noon := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, loc).Zone()
	_, end := time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc).Zone()
	return start != noon || noon != end
}

// firingsOn returns the firing instants of one local calendar day, sorted
// and coalesced. day carries only the date (as UTC midnight).
func (c cronSpec) firingsOn(day time.Time, loc *time.Location) []time.Time {
	if !c.matchesDay(day) {
		return nil
	}
	var out []time.Time
	for h := 0; h < 24; h++ {
		if c.hour&(1<<h) == 0 {
			continue
		}
		for m := 0; m < 60; m++ {
			if c.minute&(1<<m) == 0 {
				continue
			}
			at := wallInstants(day.Year(), day.Month(), day.Day(), h, m, loc)
			switch {
			case len(at) == 0:
				out = append(out, gapEnd(day.Year(), day.Month(), day.Day(), h, m, loc))
			case c.wildcard:
				out = append(out, at...)
			default:
				out = append(out, at[0])
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	coalesced := out[:0]
	for _, t := range out {
		if len(coalesced) == 0 || !t.Equal(coalesced[len(coalesced)-1]) {
			coalesced = append(coalesced, t)
		}
	}
	return coalesced
}

// zoneOffsetsNear is every UTC offset loc uses within a day of the wall
// time w (read as UTC). An instant showing w is within 14 hours of it.
func zoneOffsetsNear(w time.Time, loc *time.Location) []int {
	var offs []int
	for _, probe := range []time.Duration{-26 * time.Hour, -14 * time.Hour, 0, 14 * time.Hour, 26 * time.Hour} {
		_, off := w.Add(probe).In(loc).Zone()
		seen := false
		for _, o := range offs {
			seen = seen || o == off
		}
		if !seen {
			offs = append(offs, off)
		}
	}
	return offs
}

// wallInstants returns the instants, ascending, at which loc's clock reads
// the given wall time: none in a spring gap, two in a fall overlap.
func wallInstants(y int, mo time.Month, d, h, mi int, loc *time.Location) []time.Time {
	w := time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
	var out []time.Time
	for _, off := range zoneOffsetsNear(w, loc) {
		u := w.Add(-time.Duration(off) * time.Second)
		l := u.In(loc)
		if l.Year() == y && l.Month() == mo && l.Day() == d && l.Hour() == h && l.Minute() == mi {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// gapEnd returns the first instant after the spring gap that swallows the
// wall time: the moment the offset changes.
func gapEnd(y int, mo time.Month, d, h, mi int, loc *time.Location) time.Time {
	w := time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
	offs := zoneOffsetsNear(w, loc)
	lowOff, highOff := offs[0], offs[0]
	for _, o := range offs {
		lowOff, highOff = min(lowOff, o), max(highOff, o)
	}
	// Under the old (lower) offset the wall time reads after the change;
	// under the new one it reads before it. The change lies between.
	lo := w.Add(-time.Duration(highOff) * time.Second)
	hi := w.Add(-time.Duration(lowOff) * time.Second)
	for hi.Sub(lo) > time.Second {
		mid := lo.Add(hi.Sub(lo) / 2)
		if _, off := mid.In(loc).Zone(); off == lowOff {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi.Truncate(time.Second)
}

// FiringID names a firing by its nominal due time in UTC, so two firings
// of one role never share an id and a run's firing is readable.
func FiringID(due time.Time) string {
	return due.UTC().Format("20060102T150405Z")
}

// OccupiesFiringSlot is the replica slot rule for a scheduled role
// (ADR-010 Amendment 1): a session occupies a slot only if it belongs to
// the role's current firing and is live or succeeded. A failed or crashed
// run holds nothing; the firing record decides whether it is retried. A
// run of an earlier firing holds nothing, live or not, which is what lets
// concurrency = allow start the next run beside it. It still counts as a
// process everywhere CountsAsAlive is asked.
func OccupiesFiringSlot(s Session, firing string) bool {
	if firing == "" || s.Firing != firing {
		return false
	}
	return s.State.CountsAsAlive() || s.State == SessionSucceeded
}

// CountFiringSlots counts the sessions that occupy a slot of firing.
func CountFiringSlots(sessions []Session, firing string) int {
	n := 0
	for i := range sessions {
		if OccupiesFiringSlot(sessions[i], firing) {
			n++
		}
	}
	return n
}

// Retry backoff inside one firing: 30s, doubling, capped at 5m, the same
// ladder as a role's crash-loop backoff.
const (
	scheduleRetryBackoffInitial = 30 * time.Second
	scheduleRetryBackoffMax     = 5 * time.Minute
)

func scheduleRetryBackoff(attempt int) time.Duration {
	d := scheduleRetryBackoffInitial
	for i := 1; i < attempt && d < scheduleRetryBackoffMax; i++ {
		d *= 2
	}
	return min(d, scheduleRetryBackoffMax)
}

// SettleRun applies a finished run to the current firing (design section
// 2, the section 11 ruling) and reports whether it froze the schedule.
//
//   - A failed run spends an attempt. While attempts <= retries and the
//     firing is inside starting_deadline (when set), a retry waits a
//     backoff. Otherwise the firing settles, and on_failure = freeze
//     freezes the schedule. An unset starting_deadline does not bound
//     retries in time: the one-minute grace a first start gets does not
//     apply to them (operator ruling on default 2, 2026-10-07).
//   - A cancelled run settles its firing: the kill was meant to stop it.
//   - A success, or a run of an earlier firing, changes nothing.
func (st *ScheduleStatus) SettleRun(r RunRecord, p SchedulePolicy, now time.Time) bool {
	if st.Firing == "" || r.Firing != st.Firing {
		return false
	}
	switch r.Outcome {
	case RunCancelled:
		st.Settled = true
		st.RetryAfter = time.Time{}
		return false
	case RunFailed:
	default:
		return false
	}
	st.Attempts++
	inDeadline := p.StartingDeadline == 0 || !now.After(st.FiringDueAt.Add(p.StartingDeadline))
	if st.Attempts <= p.Retries && inDeadline {
		st.RetryAfter = now.Add(scheduleRetryBackoff(st.Attempts))
		return false
	}
	st.Settled = true
	st.RetryAfter = time.Time{}
	if p.OnFailure == ScheduleOnFailureFreeze && !st.Frozen {
		st.Frozen = true
		return true
	}
	return false
}

// ScheduleZoneFacts reports, for a next-due computation from from to
// next, the offset change between them as text (empty when none), and
// whether the zone observes daylight saving over the coming year. An
// unloadable zone reports nothing; parse has already refused it.
func ScheduleZoneFacts(tz string, from, next time.Time) (shift string, observes bool) {
	loc, err := loadScheduleZone(tz)
	if err != nil {
		return "", false
	}
	_, before := from.In(loc).Zone()
	_, after := next.In(loc).Zone()
	if before != after {
		shift = fmt.Sprintf("timezone %s: UTC offset changes from %s to %s", tz, formatOffset(before), formatOffset(after))
	}
	_, _, observes = zoneOffsets(loc, from)
	return shift, observes
}
