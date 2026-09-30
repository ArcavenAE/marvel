package api

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A schedule turns a headless role (a Job, ADR-010) into a CronJob
// (ADR-010 Amendment 1). This file is S-1 of docs/design/scheduled-runs.md:
// the block is parsed and validated here, and carried on the Role. Nothing
// fires yet; the clock is S-3.

// Concurrency policies: what a firing does when the previous run is live.
const (
	ScheduleConcurrencyForbid  = "forbid"
	ScheduleConcurrencyReplace = "replace"
	ScheduleConcurrencyAllow   = "allow"
)

// On-failure policies, applied once a firing's retries are used up.
const (
	ScheduleOnFailureWait   = "wait"
	ScheduleOnFailureFreeze = "freeze"
)

// ManifestSchedule is the schedule section within a role.
type ManifestSchedule struct {
	Cron     string `toml:"cron"                        yaml:"cron"`
	Timezone string `toml:"timezone"                    yaml:"timezone"`
	// DSTAck keeps a zone that observes daylight saving (design 2a).
	DSTAck           bool             `toml:"dst_ack,omitempty"           yaml:"dst_ack,omitempty"`
	Concurrency      string           `toml:"concurrency,omitempty"       yaml:"concurrency,omitempty"`
	StartingDeadline string           `toml:"starting_deadline,omitempty" yaml:"starting_deadline,omitempty"`
	ActiveDeadline   string           `toml:"active_deadline,omitempty"   yaml:"active_deadline,omitempty"`
	Jitter           string           `toml:"jitter,omitempty"            yaml:"jitter,omitempty"`
	StaleAfter       string           `toml:"stale_after,omitempty"       yaml:"stale_after,omitempty"`
	Retries          int              `toml:"retries,omitempty"           yaml:"retries,omitempty"`
	OnFailure        string           `toml:"on_failure,omitempty"        yaml:"on_failure,omitempty"`
	History          *ManifestHistory `toml:"history,omitempty"           yaml:"history,omitempty"`
	Suspend          bool             `toml:"suspend,omitempty"           yaml:"suspend,omitempty"`
}

// ManifestHistory is how many finished runs of each outcome to keep.
type ManifestHistory struct {
	Succeeded int `toml:"succeeded" yaml:"succeeded"`
	Failed    int `toml:"failed"    yaml:"failed"`
}

// SchedulePolicy is a validated schedule, carried on Role.Schedule. A zero
// duration means the field was unset.
type SchedulePolicy struct {
	Cron             string        `json:"cron"`
	Timezone         string        `json:"timezone"`
	DSTAck           bool          `json:"dst_ack,omitempty"`
	Concurrency      string        `json:"concurrency"`
	StartingDeadline time.Duration `json:"starting_deadline,omitempty"`
	ActiveDeadline   time.Duration `json:"active_deadline,omitempty"`
	Jitter           time.Duration `json:"jitter,omitempty"`
	StaleAfter       time.Duration `json:"stale_after,omitempty"`
	Retries          int           `json:"retries,omitempty"`
	OnFailure        string        `json:"on_failure"`
	// History is always set on a parsed policy (default 3 and 3). A record
	// written by an earlier build may carry nil.
	History *ScheduleHistory `json:"history,omitempty"`
	Suspend bool             `json:"suspend,omitempty"`
}

// ScheduleHistory is how many finished runs of each outcome to keep.
type ScheduleHistory struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

// DSTAcknowledgement names a schedule accepted in a zone that observes
// daylight saving because it carries dst_ack; apply reports each one as
// schedule.dst-acknowledged.
type DSTAcknowledgement struct {
	Team     string
	Role     string
	Timezone string
}

// validateSchedule checks one role's schedule block. now anchors the
// daylight-saving scan.
func validateSchedule(team string, r ManifestRole, now time.Time) error {
	s := r.Schedule
	where := fmt.Sprintf("team %s role %s", team, r.Name)
	if r.Runtime.Mode != RuntimeModeHeadless {
		return fmt.Errorf("%s: schedule is only valid on a headless role (runtime.mode = %q); an interactive role has no run to start", where, RuntimeModeHeadless)
	}
	// ADR-010 Amendment 1: neither applies to a scheduled role, so setting
	// one would silently do nothing.
	if r.RestartPolicy != "" {
		return fmt.Errorf("%s: restart_policy does not apply to a scheduled role; use schedule.retries and schedule.on_failure", where)
	}
	if r.MaxRestarts != 0 {
		return fmt.Errorf("%s: max_restarts does not apply to a scheduled role; use schedule.retries and schedule.on_failure", where)
	}
	if s.Cron == "" {
		return fmt.Errorf("%s: schedule.cron is required", where)
	}
	fields, err := parseCron(s.Cron)
	if err != nil {
		return fmt.Errorf("%s: schedule.%w", where, err)
	}
	if s.Timezone == "" {
		return fmt.Errorf("%s: schedule.timezone is required and has no default; name an IANA zone (\"Etc/UTC\") or a fixed offset (\"-06:00\")", where)
	}
	loc, err := loadScheduleZone(s.Timezone)
	if err != nil {
		return fmt.Errorf("%s: schedule.%w", where, err)
	}
	if lower, higher, observes := zoneOffsets(loc, now); observes && !s.DSTAck {
		return fmt.Errorf("%s: %s", where, dstRefusal(s.Timezone, fields, lower, higher))
	}
	switch s.Concurrency {
	case "", ScheduleConcurrencyForbid, ScheduleConcurrencyReplace, ScheduleConcurrencyAllow:
	default:
		return fmt.Errorf("%s: schedule.concurrency %q is not valid (valid: %s, %s, %s)", where, s.Concurrency, ScheduleConcurrencyForbid, ScheduleConcurrencyReplace, ScheduleConcurrencyAllow)
	}
	switch s.OnFailure {
	case "", ScheduleOnFailureWait, ScheduleOnFailureFreeze:
	default:
		return fmt.Errorf("%s: schedule.on_failure %q is not valid (valid: %s, %s)", where, s.OnFailure, ScheduleOnFailureWait, ScheduleOnFailureFreeze)
	}
	if s.Retries < 0 {
		return fmt.Errorf("%s: schedule.retries must be >= 0", where)
	}
	// Required: a run with no wall-clock bound is how a scheduled job
	// spends overnight (design section 4), and a schedule with no freshness
	// bound fails silently (section 7).
	if s.ActiveDeadline == "" {
		return fmt.Errorf("%s: schedule.active_deadline is required; it bounds each run's wall-clock time", where)
	}
	if s.StaleAfter == "" {
		return fmt.Errorf("%s: schedule.stale_after is required; it raises schedule.stale when no run has succeeded for that long", where)
	}
	for _, d := range []struct{ name, value string }{
		{"starting_deadline", s.StartingDeadline},
		{"active_deadline", s.ActiveDeadline},
		{"jitter", s.Jitter},
		{"stale_after", s.StaleAfter},
	} {
		if _, err := scheduleDuration(d.name, d.value); err != nil {
			return fmt.Errorf("%s: schedule.%w", where, err)
		}
	}
	if h := s.History; h != nil {
		if h.Succeeded <= 0 {
			return fmt.Errorf("%s: schedule.history.succeeded must be > 0 when set", where)
		}
		if h.Failed <= 0 {
			return fmt.Errorf("%s: schedule.history.failed must be > 0 when set", where)
		}
	}
	return nil
}

// Defaults for what the design leaves open, ruled by the architect
// 2026-09-30: unset history keeps this many runs of each outcome.
const (
	defaultScheduleHistorySucceeded = 3
	defaultScheduleHistoryFailed    = 3
)

// policy converts a validated block. Defaults: concurrency forbid,
// on_failure wait and retries 0 (design section 11); history 3 and 3.
// Unset jitter and starting_deadline stay zero: no delay, and no catch-up
// of a missed firing (S-3 enforces that).
func (s ManifestSchedule) policy() (*SchedulePolicy, error) {
	p := &SchedulePolicy{
		Cron:        s.Cron,
		Timezone:    s.Timezone,
		DSTAck:      s.DSTAck,
		Concurrency: s.Concurrency,
		Retries:     s.Retries,
		OnFailure:   s.OnFailure,
		Suspend:     s.Suspend,
	}
	if p.Concurrency == "" {
		p.Concurrency = ScheduleConcurrencyForbid
	}
	if p.OnFailure == "" {
		p.OnFailure = ScheduleOnFailureWait
	}
	for _, d := range []struct {
		name, value string
		dst         *time.Duration
	}{
		{"starting_deadline", s.StartingDeadline, &p.StartingDeadline},
		{"active_deadline", s.ActiveDeadline, &p.ActiveDeadline},
		{"jitter", s.Jitter, &p.Jitter},
		{"stale_after", s.StaleAfter, &p.StaleAfter},
	} {
		v, err := scheduleDuration(d.name, d.value)
		if err != nil {
			return nil, err
		}
		*d.dst = v
	}
	p.History = &ScheduleHistory{Succeeded: defaultScheduleHistorySucceeded, Failed: defaultScheduleHistoryFailed}
	if s.History != nil {
		p.History = &ScheduleHistory{Succeeded: s.History.Succeeded, Failed: s.History.Failed}
	}
	return p, nil
}

// scheduleDuration parses an optional duration; set means positive.
func scheduleDuration(name, value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a duration: %w", name, value, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be > 0 when set (got %q)", name, value)
	}
	return d, nil
}

// DSTAcknowledgements lists the manifest's schedules kept in a zone that
// observes daylight saving because they carry dst_ack.
func (m *Manifest) DSTAcknowledgements(now time.Time) []DSTAcknowledgement {
	var out []DSTAcknowledgement
	for _, t := range m.Teams {
		for _, r := range t.Roles {
			if r.Schedule == nil || !r.Schedule.DSTAck {
				continue
			}
			loc, err := loadScheduleZone(r.Schedule.Timezone)
			if err != nil {
				continue
			}
			if _, _, observes := zoneOffsets(loc, now); observes {
				out = append(out, DSTAcknowledgement{Team: t.Name, Role: r.Name, Timezone: r.Schedule.Timezone})
			}
		}
	}
	return out
}

// loadScheduleZone resolves an IANA zone name or a fixed "+HH:MM" offset.
// "Local" is refused: it is the daemon host's zone, a default in disguise.
func loadScheduleZone(tz string) (*time.Location, error) {
	if tz == "Local" {
		return nil, fmt.Errorf("timezone %q is the daemon host's zone, which is a hidden default; name an IANA zone or a fixed offset", tz)
	}
	if strings.HasPrefix(tz, "+") || strings.HasPrefix(tz, "-") {
		secs, ok := parseFixedOffset(tz)
		if !ok {
			return nil, fmt.Errorf("timezone %q is not a fixed offset of the form \"+HH:MM\" or \"-HH:MM\"", tz)
		}
		return time.FixedZone(tz, secs), nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("timezone %q is not an IANA zone or a fixed offset like \"-06:00\": %w", tz, err)
	}
	return loc, nil
}

// parseFixedOffset reads "+HH:MM" or "-HH:MM" into seconds east of UTC.
func parseFixedOffset(s string) (int, bool) {
	if len(s) != 6 || s[3] != ':' {
		return 0, false
	}
	h, errH := strconv.Atoi(s[1:3])
	m, errM := strconv.Atoi(s[4:6])
	if errH != nil || errM != nil || h > 14 || m > 59 {
		return 0, false
	}
	secs := h*3600 + m*60
	if s[0] == '-' {
		secs = -secs
	}
	return secs, true
}

// zoneOffsets reports the lowest and highest UTC offsets loc uses in the
// twelve months from now, and whether they differ. Detection is by offset
// change, not IsDST: tzdata models some zones' winter as negative daylight
// saving (Europe/Dublin), where the "standard" flag points at summer.
func zoneOffsets(loc *time.Location, now time.Time) (lower, higher int, observes bool) {
	_, lower = now.In(loc).Zone()
	higher = lower
	for h := 6; h <= 366*24; h += 6 {
		_, off := now.Add(time.Duration(h) * time.Hour).In(loc).Zone()
		lower = min(lower, off)
		higher = max(higher, off)
	}
	return lower, higher, lower != higher
}

// dstRefusal is the design 2a refusal, computed for this schedule. The
// fixed alternative is the zone's lower offset, its winter time in either
// hemisphere.
func dstRefusal(tz string, fields []string, lower, higher int) string {
	shift := higher - lower
	var b strings.Builder
	fmt.Fprintf(&b, "timezone %s observes daylight saving; its firings move by %s against UTC twice a year.\n", tz, formatShift(shift))
	fmt.Fprintf(&b, "  fixed alternative: timezone = %q", formatOffset(lower))
	minute, hour, single := singleTime(fields)
	if single {
		local := hour*60 + minute
		fmt.Fprintf(&b, " (fires at %s local in winter and %s local in summer)", clock(local), clock(local+shift/60))
	}
	b.WriteString("\n")
	if utc, ok := utcCron(fields, lower); ok {
		fmt.Fprintf(&b, "  or UTC:            timezone = \"Etc/UTC\", cron = %q\n", utc)
	} else {
		fmt.Fprintf(&b, "  or UTC:            timezone = \"Etc/UTC\", and convert the cron by hand from UTC%s\n", formatOffset(lower))
	}
	b.WriteString("  or keep DST:       add dst_ack = true")
	return b.String()
}

// utcCron rewrites a cron with a single minute and hour from the given
// offset to UTC. It declines when the time is not single, or when the move
// crosses midnight and a day field is set, since the day would change too.
func utcCron(fields []string, offset int) (string, bool) {
	minute, hour, single := singleTime(fields)
	if !single || offset%60 != 0 {
		return "", false
	}
	total := hour*60 + minute - offset/60
	if total < 0 || total >= 24*60 {
		if fields[2] != "*" || fields[3] != "*" || fields[4] != "*" {
			return "", false
		}
		total = ((total % (24 * 60)) + 24*60) % (24 * 60)
	}
	out := append([]string{strconv.Itoa(total % 60), strconv.Itoa(total / 60)}, fields[2:]...)
	return strings.Join(out, " "), true
}

// singleTime reports the cron's minute and hour when both are one number.
func singleTime(fields []string) (minute, hour int, ok bool) {
	m, errM := strconv.Atoi(fields[0])
	h, errH := strconv.Atoi(fields[1])
	return m, h, errM == nil && errH == nil
}

// clock renders minutes past midnight as HH:MM, wrapping past a day.
func clock(minutes int) string {
	minutes = ((minutes % (24 * 60)) + 24*60) % (24 * 60)
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// formatOffset renders seconds east of UTC as "+HH:MM".
func formatOffset(secs int) string {
	sign := "+"
	if secs < 0 {
		sign, secs = "-", -secs
	}
	return fmt.Sprintf("%s%02d:%02d", sign, secs/3600, secs%3600/60)
}

// formatShift renders an offset difference as "1h", "30m" or "1h30m".
func formatShift(secs int) string {
	h, m := secs/3600, secs%3600/60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

// cronFields are the five fields, in order, with their inclusive bounds.
// Day-of-week takes 0 or 7 for Sunday.
var cronFields = []struct {
	name     string
	min, max int
}{
	{"minute", 0, 59},
	{"hour", 0, 23},
	{"day-of-month", 1, 31},
	{"month", 1, 12},
	{"day-of-week", 0, 7},
}

// parseCron checks a five-field numeric cron: each field is a comma list
// of "*", "n" or "n-m", each optionally with "/step". Names and
// descriptors such as "@daily" are refused rather than guessed at.
func parseCron(expr string) ([]string, error) {
	fields := strings.Fields(expr)
	if len(fields) != len(cronFields) {
		return nil, fmt.Errorf("cron %q must have five fields (minute hour day-of-month month day-of-week)", expr)
	}
	for i, f := range fields {
		spec := cronFields[i]
		for _, item := range strings.Split(f, ",") {
			if err := checkCronItem(item, spec.min, spec.max); err != nil {
				return nil, fmt.Errorf("cron %q: %s field %q: %w", expr, spec.name, f, err)
			}
		}
	}
	return fields, nil
}

func checkCronItem(item string, lo, hi int) error {
	base, step, hasStep := strings.Cut(item, "/")
	if hasStep {
		n, err := strconv.Atoi(step)
		if err != nil || n < 1 {
			return fmt.Errorf("step %q must be a positive number", step)
		}
	}
	if base == "*" {
		return nil
	}
	from, to, isRange := strings.Cut(base, "-")
	a, err := cronNumber(from, lo, hi)
	if err != nil {
		return err
	}
	if !isRange {
		return nil
	}
	b, err := cronNumber(to, lo, hi)
	if err != nil {
		return err
	}
	if a > b {
		return fmt.Errorf("range %q runs backwards", base)
	}
	return nil
}

func cronNumber(s string, lo, hi int) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%d is outside %d-%d", n, lo, hi)
	}
	return n, nil
}
