package main

import (
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// ageCell renders the AGE cell: the time since the session was created, a
// dash for a row with no creation time (a held role's synthetic row). It is a
// level that grows and never goes stale, so it needs no as-of window.
func ageCell(s api.Session, now time.Time) string {
	if s.CreatedAt.IsZero() {
		return "-"
	}
	return sessionAgeWords(now.Sub(s.CreatedAt))
}

// sessionAgeWords is ageWords with days past one day, so a seat that has run
// for days reads "3d4h" and not "76h30m".
func sessionAgeWords(d time.Duration) string {
	d = max(d, 0)
	if d < 24*time.Hour {
		return ageWords(d)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}
