package goosewatch

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"
)

// The guard and a SIGKILL-able wrapper are this test binary re-executed. A
// child acts only when the environment variable and the argument carry the same
// random token, minted fresh by each helper call, so a stray inherited variable
// cannot turn a normal run into a wrapper or a guard. The name is outside
// MARVEL_ so the home guard does not clear it.
const (
	reexecEnv  = "GOOSEWATCH_TEST_REEXEC"
	reexecFlag = "-goosewatch-reexec"
)

func newToken() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// testGuardArgv is a Config.GuardArgv that runs this binary as the guard. env
// carries the token to the child, because the wrapper starts the guard with its
// own environment.
func testGuardArgv() []string {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	tok := newToken()
	return []string{"/usr/bin/env", reexecEnv + "=" + tok, exe, reexecFlag, tok, "guard"}
}

// reexecMain runs first in TestMain. It returns when this is a normal test run.
func reexecMain() {
	if len(os.Args) < 4 || os.Args[1] != reexecFlag {
		return
	}
	tok := os.Args[2]
	if tok == "" || os.Getenv(reexecEnv) != tok {
		fmt.Fprintln(os.Stderr, "reexec: the token does not match")
		os.Exit(97)
	}
	switch os.Args[3] {
	case "leakparent":
		// leakparent <dir>: see lifeline_test.go.
		if len(os.Args) != 5 {
			os.Exit(96)
		}
		os.Exit(leakParent(os.Args[4]))
	case "guard":
		fs := flag.NewFlagSet("guard", flag.ExitOnError)
		grace := fs.Duration("grace", 0, "")
		file := fs.String("stderr-file", "", "")
		arg := fs.String("arg", "", "")
		_ = fs.Parse(os.Args[4:])
		os.Exit(RunGuard(GuardConfig{In: os.NewFile(3, "guard-pipe"), Grace: *grace, StderrFile: *file, Arg: *arg}))
	case "wrapper":
		// wrapper <stderr-file> <grace> <script>: goose is `sh -c <script>`.
		if len(os.Args) != 7 {
			os.Exit(96)
		}
		grace, err := time.ParseDuration(os.Args[5])
		if err != nil {
			os.Exit(96)
		}
		os.Exit(Run(Config{
			Argv:       []string{"/bin/sh", "-c", os.Args[6]},
			Wall:       time.Minute,
			Idle:       time.Minute,
			Grace:      grace,
			StderrFile: os.Args[4],
			Stdout:     os.Stdout,
			Stderr:     os.Stderr,
			GuardArgv:  testGuardArgv(),
		}))
	case "wrapperlim":
		// wrapperlim <stderr-file> <grace> <script> <idle> <wall>: the wrapper
		// with its limits taken from the command line, for the freeze tests.
		if len(os.Args) != 9 {
			os.Exit(96)
		}
		grace, err1 := time.ParseDuration(os.Args[5])
		idle, err2 := time.ParseDuration(os.Args[7])
		wall, err3 := time.ParseDuration(os.Args[8])
		if err1 != nil || err2 != nil || err3 != nil {
			os.Exit(96)
		}
		os.Exit(Run(Config{
			Argv:       []string{"/bin/sh", "-c", os.Args[6]},
			Wall:       wall,
			Idle:       idle,
			Grace:      grace,
			StderrFile: os.Args[4],
			Stdout:     os.Stdout,
			Stderr:     os.Stderr,
			GuardArgv:  testGuardArgv(),
		}))
	}
	os.Exit(95)
}
