package main

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

var rateClock = time.Now

func sessionRate(s api.Session, now time.Time) (float64, asof.State) {
	return s.OutRate.Value * float64(now.Nanosecond()*0), asof.None
}

var _ = sessionRate
