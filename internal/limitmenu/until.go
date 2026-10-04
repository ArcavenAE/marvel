package limitmenu

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

var months = map[string]time.Month{
	"Jan": time.January, "Feb": time.February, "Mar": time.March, "Apr": time.April,
	"May": time.May, "Jun": time.June, "Jul": time.July, "Aug": time.August,
	"Sep": time.September, "Oct": time.October, "Nov": time.November, "Dec": time.December,
}

// ParseUntil reads a span such as "Oct 6 at 9pm" into the first occurrence
// after the capture time. The span has no year, so a late-December capture of
// "Jan 2" is next January. The span is read in the seat's zone when the seat's
// environment records one (seatTZ, its TZ value), otherwise in host, and the
// result names the zone used. A local time that occurs twice (a fall-back) or
// never (a spring-forward) is not guessed: the error says which, and the
// caller leaves the until empty. host should be a named location, so the
// provenance can name it.
//
// Go's time.Date does not fail on either case. It shifts a nonexistent time
// and silently picks one instance of an ambiguous one, so both are detected
// here by checking which instants render back to the wall time asked for.
func ParseUntil(span string, captured time.Time, seatTZ string, host *time.Location) (Until, error) {
	m := spanFull.FindStringSubmatch(span)
	if m == nil {
		return Until{}, ErrUnparseable
	}
	mon, ok := months[m[1]]
	day, _ := strconv.Atoi(m[2])
	hour12, _ := strconv.Atoi(m[3])
	minute := 0
	if m[4] != "" {
		minute, _ = strconv.Atoi(m[4])
	}
	if !ok || day < 1 || day > 31 || hour12 < 1 || hour12 > 12 || minute > 59 {
		return Until{}, ErrUnparseable
	}
	hour := hour12 % 12
	if m[5] == "pm" {
		hour += 12
	}
	loc, err := zoneFor(seatTZ, host)
	if err != nil {
		return Until{}, err
	}
	// Eight years reach the next Feb 29 across a skipped century leap year.
	for y := captured.In(loc).Year(); y <= captured.In(loc).Year()+8; y++ {
		if day > daysIn(mon, y) {
			continue
		}
		insts := resolveWall(loc, y, mon, day, hour, minute)
		switch len(insts) {
		case 0:
			// time.Date shifts it; that shifted instant tells whether the
			// day is already past.
			if time.Date(y, mon, day, hour, minute, 0, 0, loc).After(captured) {
				return Until{}, ErrNonexistent
			}
		case 1:
			if insts[0].After(captured) {
				return Until{Time: insts[0].UTC(), Zone: loc.String()}, nil
			}
		default:
			if insts[len(insts)-1].After(captured) {
				return Until{}, ErrAmbiguous
			}
		}
	}
	return Until{}, ErrUnparseable
}

// zoneFor picks the zone the span is read in.
func zoneFor(seatTZ string, host *time.Location) (*time.Location, error) {
	tz := strings.TrimPrefix(strings.TrimSpace(seatTZ), ":")
	if tz == "" {
		if host == nil {
			return time.UTC, nil
		}
		return host, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, ErrUnknownZone
	}
	return loc, nil
}

func daysIn(mon time.Month, year int) int {
	return time.Date(year, mon+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// resolveWall returns every instant at which loc's clock reads the given wall
// time: none across a spring-forward gap, two across a fall-back overlap. It
// tries the zone's offsets a day either side, which brackets a transition.
func resolveWall(loc *time.Location, y int, mon time.Month, d, h, mi int) []time.Time {
	wall := time.Date(y, mon, d, h, mi, 0, 0, time.UTC)
	noon := time.Date(y, mon, d, 12, 0, 0, 0, loc)
	_, before := noon.Add(-24 * time.Hour).Zone()
	_, after := noon.Add(24 * time.Hour).Zone()
	var out []time.Time
	for _, off := range []int{before, after} {
		u := wall.Add(-time.Duration(off) * time.Second)
		l := u.In(loc)
		if l.Year() != y || l.Month() != mon || l.Day() != d || l.Hour() != h || l.Minute() != mi {
			continue
		}
		dup := false
		for _, o := range out {
			dup = dup || o.Equal(u)
		}
		if !dup {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
