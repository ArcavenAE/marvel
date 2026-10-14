package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/arcavenae/marvel/internal/paths"
)

const marvelHomeFlag = "--marvel-home"

// applyMarvelHomeFlag reads --marvel-home from the arguments and sets
// MARVEL_HOME to its value, so the flag and the variable are one selector and
// the flag wins. It runs before the command tree is built, not in
// PersistentPreRun: `marvel daemon` binds the defaults of --log-file,
// --pidfile and --state-bolt when its command is constructed, so a variable
// set after that would leave those three under the old root while the socket
// and the config moved.
//
// The scan stops at a bare "--", past which every word belongs to the command
// being run. The last occurrence wins, as it does for any flag. An empty
// value, a relative value and a root whose socket would not fit are refused
// with an error that names the value; nothing is set in that case.
func applyMarvelHomeFlag(args []string) error {
	var (
		value string
		given bool
	)
	for i := 1; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		switch {
		case a == marvelHomeFlag:
			if i+1 >= len(args) {
				return fmt.Errorf("flag needs an argument: %s", marvelHomeFlag)
			}
			i++
			value, given = args[i], true
		case strings.HasPrefix(a, marvelHomeFlag+"="):
			value, given = strings.TrimPrefix(a, marvelHomeFlag+"="), true
		}
	}
	if !given {
		return nil
	}
	l, err := paths.ParseHome(value, marvelHomeFlag)
	if err != nil {
		return err
	}
	return os.Setenv(paths.HomeEnv, l.Home)
}
