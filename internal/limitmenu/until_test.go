package limitmenu

import (
	"errors"
	"testing"
	"time"
	_ "time/tzdata" // the zone cases must not depend on the host's zoneinfo
)

func chicagoZone(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatalf("load America/Chicago: %v", err)
	}
	return loc
}

// Test 29, the pure parts: first occurrence after the capture, the zone rule,
// and the two wall times Go's time.Date would shift or pick silently.
func TestParseUntil(t *testing.T) {
	t.Parallel()
	chicago := chicagoZone(t)
	utc := func(y int, m time.Month, d, h, mi int) time.Time { return time.Date(y, m, d, h, mi, 0, 0, time.UTC) }
	tests := []struct {
		name     string
		span     string
		captured time.Time
		seatTZ   string
		want     time.Time
		wantZone string
	}{
		{"next year after a late-December capture", "Jan 2 at 9pm", utc(2026, 12, 30, 12, 0), "UTC", utc(2027, 1, 2, 21, 0), "UTC"},
		{"same year when still ahead", "Oct 6 at 9pm", utc(2026, 10, 4, 5, 0), "UTC", utc(2026, 10, 6, 21, 0), "UTC"},
		{"seat zone wins over the host zone", "Oct 6 at 2am", utc(2026, 10, 4, 5, 0), "UTC", utc(2026, 10, 6, 2, 0), "UTC"},
		{"host zone when the seat records none", "Oct 6 at 2am", utc(2026, 10, 4, 5, 0), "", utc(2026, 10, 6, 7, 0), "America/Chicago"},
		{"minutes and am", "Nov 12 at 10:30am", utc(2026, 10, 4, 5, 0), "UTC", utc(2026, 11, 12, 10, 30), "UTC"},
		{"12am is midnight", "Oct 6 at 12am", utc(2026, 10, 4, 5, 0), "UTC", utc(2026, 10, 6, 0, 0), "UTC"},
		{"12pm is noon", "Oct 6 at 12pm", utc(2026, 10, 4, 5, 0), "UTC", utc(2026, 10, 6, 12, 0), "UTC"},
		{"a capture exactly at the time reads the next year", "Oct 4 at 5am", utc(2026, 10, 4, 5, 0), "UTC", utc(2027, 10, 4, 5, 0), "UTC"},
		{"Feb 29 skips to a leap year", "Feb 29 at 9am", utc(2026, 10, 4, 5, 0), "UTC", utc(2028, 2, 29, 9, 0), "UTC"},
		{"the year is read in the zone, not in UTC", "Jan 2 at 9am", utc(2026, 12, 31, 23, 30), "Asia/Tokyo", utc(2027, 1, 2, 0, 0), "Asia/Tokyo"},
		{"a POSIX leading colon is tolerated", "Oct 6 at 2am", utc(2026, 10, 4, 5, 0), ":UTC", utc(2026, 10, 6, 2, 0), "UTC"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseUntil(tc.span, tc.captured, tc.seatTZ, chicago)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !got.Time.Equal(tc.want) || got.Time.Location() != time.UTC {
				t.Fatalf("time = %v, want %v in UTC", got.Time, tc.want)
			}
			if got.Zone != tc.wantZone {
				t.Fatalf("zone = %q, want %q", got.Zone, tc.wantZone)
			}
		})
	}
}

func TestParseUntilIsNotGuessed(t *testing.T) {
	t.Parallel()
	chicago := chicagoZone(t)
	oct := time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		span     string
		captured time.Time
		seatTZ   string
		want     error
	}{
		{"fall-back hour occurs twice", "Nov 1 at 1:30am", oct, "", ErrAmbiguous},
		{"spring-forward hour does not occur", "Mar 14 at 2:30am", oct, "", ErrNonexistent},
		{"the same wall time is fine in UTC", "Mar 14 at 2:30am", oct, "UTC", nil},
		{"unknown seat zone", "Oct 6 at 9pm", oct, "Mars/Olympus", ErrUnknownZone},
		{"not a date", "tomorrow", oct, "", ErrUnparseable},
		{"unknown month", "Foo 6 at 9pm", oct, "", ErrUnparseable},
		{"day 32", "Oct 32 at 9pm", oct, "", ErrUnparseable},
		{"day 0", "Oct 0 at 9pm", oct, "", ErrUnparseable},
		{"hour 0", "Oct 6 at 0am", oct, "", ErrUnparseable},
		{"hour 13", "Oct 6 at 13pm", oct, "", ErrUnparseable},
		{"minute 60", "Oct 6 at 9:60pm", oct, "", ErrUnparseable},
		{"trailing text", "Oct 6 at 9pm sharp", oct, "", ErrUnparseable},
		{"empty", "", oct, "", ErrUnparseable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseUntil(tc.span, tc.captured, tc.seatTZ, chicago)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !got.Time.IsZero() {
				t.Fatalf("an until was returned with the error: %v", got.Time)
			}
		})
	}
}

// A nonexistent or doubled wall time that has already passed is not the next
// occurrence; the next year's is read instead.
func TestParseUntilSkipsAPastAmbiguousTime(t *testing.T) {
	t.Parallel()
	chicago := chicagoZone(t)
	captured := time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := ParseUntil("Nov 1 at 1:30am", captured, "", chicago); err != nil {
		t.Fatalf("Nov 1 2027 is not doubled: %v", err)
	}
}

// The reviewer's case on #546: the year is read in the zone, not in UTC. At
// 2027-01-01T03:00Z it is still 21:00 on Dec 31 2026 in Chicago, so "Dec 31 at
// 11pm" is two hours ahead in 2026, not eleven months ahead in 2027.
func TestParseUntilReadsTheYearInTheZone(t *testing.T) {
	t.Parallel()
	chicago := chicagoZone(t)
	captured := time.Date(2027, 1, 1, 3, 0, 0, 0, time.UTC)
	got, err := ParseUntil("Dec 31 at 11pm", captured, "", chicago)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if want := time.Date(2027, 1, 1, 5, 0, 0, 0, time.UTC); !got.Time.Equal(want) {
		t.Fatalf("time = %v, want %v (Dec 31 2026 23:00 CST)", got.Time, want)
	}
}
