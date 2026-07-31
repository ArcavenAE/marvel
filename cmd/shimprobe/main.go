// Command shimprobe carries the test children and test clients used by the
// marvel-shim spike (aae-orc-e35c). Each subcommand exists to exercise one of
// the five pre-declared success signals; none of it is production code.
//
//	shimprobe winsize          child that reports its window size on SIGWINCH
//	shimprobe spew -n N        child that writes N numbered ANSI lines
//	shimprobe ttyprobe         child that queries the terminal and waits (falsification)
//	shimprobe echo             child that echoes stdin lines with a marker
//	shimprobe sink -s PATH     stream-socket consumer, counts and verifies lines
//	shimprobe ctl -c PATH ...  control-socket client
//	shimprobe rtt -mode M      inject-to-observe round trip, single PTY vs shim
//	shimprobe tee -s PATH      dump the stream socket verbatim
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: shimprobe {winsize|spew|ttyprobe|echo|sink|ctl|rtt|tee} [flags]")
		os.Exit(2)
	}
	sub := os.Args[1]
	args := os.Args[2:]

	var err error
	switch sub {
	case "winsize":
		err = runWinsize(args)
	case "spew":
		err = runSpew(args)
	case "ttyprobe":
		err = runTTYProbe(args)
	case "echo":
		err = runEcho(args)
	case "sink":
		err = runSink(args)
	case "ctl":
		err = runCtl(args)
	case "rtt":
		err = runRTT(args)
	case "tee":
		err = runTee(args)
	default:
		err = fmt.Errorf("unknown subcommand %q", sub)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "shimprobe %s: %v\n", sub, err)
		os.Exit(1)
	}
}
