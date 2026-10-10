// Command watcherprobe is the logger and checker half of the watcher's claude
// probe kit (scripts/probes/watcher-claude). It runs only against a scratch
// claude the kit started; it reads no marvel state and talks to no daemon.
//
//	watcherprobe hook       --log FILE    log one hook firing (stdin is the payload)
//	watcherprobe statusline --log FILE    log one statusline refresh
//	watcherprobe mark       --log FILE NAME [BODY]
//	watcherprobe mono                     print the monotonic clock
//	watcherprobe mask                     copy stdin to stdout with key text hidden;
//	                                      the secret is in WATCHER_PROBE_MASK
//	watcherprobe check      --log FILE --out FILE --stamp TEXT
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/arcavenae/marvel/internal/watcherprobe"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: watcherprobe hook|statusline|mark|mono|mask|check ...")
		return 2
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	logPath := fs.String("log", "", "the event log")
	out := fs.String("out", "", "where check writes probe_result.json")
	stamp := fs.String("stamp", "", "the claude build stamp the result carries")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	needLog := func() bool {
		if *logPath == "" {
			_, _ = fmt.Fprintf(stderr, "watcherprobe %s: --log is required\n", args[0])
			return false
		}
		return true
	}
	switch args[0] {
	case "hook":
		// A hook command prints nothing and exits zero whatever happens, so the
		// probe can never change what it measures.
		if needLog() {
			if err := watcherprobe.RecordHook(*logPath, stdin, os.Environ()); err != nil {
				_, _ = fmt.Fprintln(stderr, "watcherprobe hook:", err)
			}
		}
		return 0
	case "statusline":
		if !needLog() {
			return 2
		}
		line, err := watcherprobe.RecordStatusline(*logPath, stdin)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "watcherprobe statusline:", err)
		}
		_, _ = fmt.Fprintln(stdout, line)
		return 0
	case "mark":
		rest := fs.Args()
		if !needLog() || len(rest) < 1 || len(rest) > 2 {
			_, _ = fmt.Fprintln(stderr, "usage: watcherprobe mark --log FILE NAME [BODY]")
			return 2
		}
		body := ""
		if len(rest) == 2 {
			body = rest[1]
		}
		if err := watcherprobe.Append(*logPath, watcherprobe.Line{MonoNS: watcherprobe.MonoNS(), Kind: watcherprobe.KindMark, Name: rest[0], Body: body}); err != nil {
			_, _ = fmt.Fprintln(stderr, "watcherprobe mark:", err)
			return 1
		}
		return 0
	case "mask":
		b, err := io.ReadAll(stdin)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "watcherprobe mask:", err)
			return 1
		}
		_, _ = io.WriteString(stdout, watcherprobe.Mask(string(b), os.Getenv("WATCHER_PROBE_MASK")))
		return 0
	case "mono":
		_, _ = fmt.Fprintln(stdout, watcherprobe.MonoNS())
		return 0
	case "check":
		if !needLog() || *out == "" {
			_, _ = fmt.Fprintln(stderr, "usage: watcherprobe check --log FILE --out FILE --stamp TEXT")
			return 2
		}
		if err := check(*logPath, *out, *stamp); err != nil {
			_, _ = fmt.Fprintln(stderr, "watcherprobe check:", err)
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprintf(stderr, "watcherprobe: unknown command %q\n", args[0])
	return 2
}

// check reads the log and writes probe_result.json by rename, so a reader never
// sees half a file.
func check(logPath, outPath, stamp string) error {
	f, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	lines, err := watcherprobe.Parse(f)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(watcherprobe.Run(lines, stamp), "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(outPath), ".probe_result-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), outPath)
}
