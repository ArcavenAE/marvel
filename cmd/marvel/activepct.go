package main

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// activePctAsOf is the ACTIVE% reading as an as-of cell.
func activePctAsOf(s api.Session, now time.Time) asof.Cell[float64] { return asof.Cell[float64]{} }

// activePctCell renders the ACTIVE% cell.
func activePctCell(s api.Session, now time.Time) string { return asof.DashNone }
