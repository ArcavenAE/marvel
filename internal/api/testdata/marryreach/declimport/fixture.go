// Package fixture: a declaration Marry reaches logs, in a file whose other
// declarations also open a store.
package fixture

import (
	"log"
	"os"
)

func CellName(duty string) string {
	log.Printf("cell %s", duty)
	return "duty." + duty
}

// Open is not reached from Marry.
func Open(path string) (*os.File, error) { return os.Open(path) }

func Marry() string { return CellName("first-turn") }
