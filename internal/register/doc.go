// Package register builds the seat register: one row for each seat, made of
// cells that report what marvel observed, each with its age
// (docs/design/seat-register.md). A row is a fact table. It holds no judgment
// of whether a seat is free, ready or in scope: those are the reader's.
//
// Every cell is an asof.Cell and renders three ways: a value while fresh, "?"
// once expired, and "-" when never observed. A cell that records what the
// build declares, rather than something checked, renders as the word
// "declared". No cell ever renders "ok", "free" or "ready".
package register
