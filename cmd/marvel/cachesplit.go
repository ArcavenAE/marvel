package main

import (
	"fmt"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// The cache split of PROMPT (I-477 slice 2, docs/design/get-sessions-output.md
// section 4.3): TIN is fresh input, CR and CW are cache reads and writes, and
// TIN + CR + CW = PROMPT on every harness. A class the producer never reported
// prints "-", never 0; a metered zero prints "0". A sum that is a lower bound
// ends in "+", as TOUT does.

// tinCell is the TIN cell.
func tinCell(s api.Session) string { return toutMarked(s.SpendFresh, s.SpendPartial) }

// crCell is the CR cell.
func crCell(s api.Session) string { return toutMarked(s.SpendCacheRead, s.SpendPartial) }

// cwCell is the CW cell. Codex reports no cache write, so its cell stays "-".
func cwCell(s api.Session) string { return toutMarked(s.SpendCacheWrite, s.SpendPartial) }

// hitPctCell is the HIT% cell: CR / PROMPT, "-" when either is unknown or the
// prompt is zero. A partial sum is the same records on both sides of the ratio,
// so the cell carries no mark.
func hitPctCell(s api.Session) string {
	if s.SpendCacheRead == nil || s.SpendPromptTokens == nil || *s.SpendPromptTokens <= 0 {
		return asof.DashNone
	}
	return fmt.Sprintf("%.0f%%", 100*float64(*s.SpendCacheRead)/float64(*s.SpendPromptTokens))
}
