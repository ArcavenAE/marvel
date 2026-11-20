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

// Rows returns the table.
func Rows() []Row { return nil }
