// Package firstrun is the table of harness screens marvel answers or parks.
package firstrun

// Action is what marvel does at a screen.
type Action string

const (
	// ActionKey answers the screen with one fixed key.
	ActionKey Action = "key"
	// ActionPark answers nothing and holds the seat for a person.
	ActionPark Action = "park"
)

// Row is one screen of the table.
type Row struct {
	ID      string
	Screen  string
	Pattern string
	Action  Action
	Answer  string
}

// Rows returns the table. It carries the usage-limit row only. Each further
// screen joins it in its own change, once a capture of that screen exists on a
// scratch harness at the version the fleet runs; a pattern is never written by
// hand (internal/panestate/patterns/README.md).
func Rows() []Row {
	return []Row{{
		ID:      "usage-limit",
		Screen:  "claude usage-limit menu",
		Pattern: "limitmenu samples (panemenu.Samples.Menus), matched by the pane-menu source",
		Action:  ActionKey,
		Answer:  "option 2, Wait here, then continue automatically (internal/limitact)",
	}}
}
