package main

import "github.com/arcavenae/marvel/internal/api"

// The cache split of PROMPT (I-477 slice 2, docs/design/get-sessions-output.md
// section 4.3): TIN is fresh input, CR and CW are cache reads and writes, and
// TIN + CR + CW = PROMPT on every harness. A class the producer never reported
// prints "-", never 0; a metered zero prints "0".

// tinCell is the TIN cell.
func tinCell(s api.Session) string { return "" }

// crCell is the CR cell.
func crCell(s api.Session) string { return "" }

// cwCell is the CW cell.
func cwCell(s api.Session) string { return "" }

// hitPctCell is the HIT% cell: CR / PROMPT, "-" when PROMPT is unknown.
func hitPctCell(s api.Session) string { return "" }
