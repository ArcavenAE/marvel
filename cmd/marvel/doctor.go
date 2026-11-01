package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/service"
)

// natsServerMinVersion is the oldest nats-server doctor accepts for a managed
// bus. The fleet's measurements ran on v2.14.x and v2.15.0 (mise.toml pins the
// latter); nothing here measured an older release, so 2.14.0 is the floor the
// evidence supports, not a statement that 2.13 fails.
const natsServerMinVersion = "2.14.0"

// doctorInput is everything the checks read from the host. The command fills it
// from the real host; a test fills it with fakes, so no check dials, execs or
// reads the host beyond what the test hands it.
type doctorInput struct {
	// LookPath resolves a program on PATH.
	LookPath func(name string) (string, error)
	// Run runs a program and returns its combined output. Only `tmux -V`,
	// `nats-server --version` and a harness's own status command go through it.
	Run func(name string, args ...string) (string, error)
	// Home is the marvel home whose permissions keys doctor audits, read-only.
	Home paths.Layout
	// Socket is the daemon address the length check reads.
	Socket string
	// Manifest is the manifest the user named, or nil.
	Manifest *api.Manifest
	// Clusters are the clusters in the client config.
	Clusters []config.Cluster
	// Sessions reads the live seats with their PANE word, or fails when the
	// daemon does not answer. Nil means no daemon read is attempted.
	Sessions func() ([]api.Session, error)
}

const (
	statPass = "PASS"
	statFail = "FAIL"
	statSkip = "SKIP"
	statWait = "WAIT"
)

// doctorReport collects the lines and counts the failures.
type doctorReport struct {
	w      io.Writer
	failed int
}

// line writes one check line, and a fix line under a failure.
func (r *doctorReport) line(status, name, detail, fix string) {
	_, _ = fmt.Fprintf(r.w, "%-4s  %-14s %s\n", status, name, detail)
	if status == statFail {
		r.failed++
		if fix != "" {
			_, _ = fmt.Fprintf(r.w, "      fix: %s\n", fix)
		}
	}
}

// runDoctor runs every check the input turns on, writes one line per check, and
// returns the number of failures. It writes nothing but to w and changes no
// setting: it is a diagnostic, and nothing in marvel reads what it returns but
// doctorExit, which the user's own scripts see as the exit code.
func runDoctor(w io.Writer, in doctorInput) int {
	r := &doctorReport{w: w}
	checkTmux(r, in)
	checkRuntimes(r, in)
	checkLogins(r, in)
	checkSocket(r, in)
	checkNats(r, in)
	checkShim(r, in)
	checkKeys(r, in)
	checkSeats(r, in)
	return r.failed
}

// doctorExit maps the failure count to the exit code: nonzero on a failure, for
// the user's own scripts. No marvel command runs the doctor and reads this.
func doctorExit(failed int) error {
	if failed == 0 {
		return nil
	}
	return &exitError{Code: 1, Msg: fmt.Sprintf("%d check(s) failed", failed)}
}

func checkTmux(r *doctorReport, in doctorInput) {
	if _, err := in.LookPath("tmux"); err != nil {
		r.line(statFail, "tmux", "not found on PATH", "brew install tmux")
		return
	}
	out, err := in.Run("tmux", "-V")
	if err != nil {
		r.line(statFail, "tmux", "found, but `tmux -V` did not answer", "reinstall it: brew reinstall tmux")
		return
	}
	r.line(statPass, "tmux", strings.TrimSpace(out), "")
}

func checkRuntimes(r *doctorReport, in doctorInput) {
	if in.Manifest == nil {
		r.line(statSkip, "harness", "no manifest named; pass one to check its harness commands", "")
		return
	}
	advisories, err := in.Manifest.ValidateRuntimes()
	if err != nil {
		r.line(statFail, "harness", "a role's command does not resolve", "install the missing program, or correct the role's runtime.command")
		for _, l := range strings.Split(redactCommands(in.Manifest, err.Error()), "\n")[1:] {
			_, _ = fmt.Fprintf(r.w, "      %s\n", strings.TrimSpace(l))
		}
		return
	}
	r.line(statPass, "harness", "every role's command resolves", "")
	for _, a := range advisories {
		_, _ = fmt.Fprintf(r.w, "      note: %s\n", a)
	}
}

// redactCommands cuts every role's quoted command in text down to its first word.
// A command can carry a flag value or a token, and ValidateRuntimes quotes the
// whole command in its error; the first word is what it resolved.
func redactCommands(m *api.Manifest, text string) string {
	for _, t := range m.Teams {
		for _, role := range t.Roles {
			cmd := role.Runtime.Command
			f := strings.Fields(cmd)
			if len(f) < 2 {
				continue
			}
			text = strings.ReplaceAll(text, strconv.Quote(cmd), strconv.Quote(f[0]+" ..."))
		}
	}
	return text
}

// harnessStatusCommands maps a harness program to its own login status command.
// doctor asks only that command and never reads a credential file. A harness
// absent from the map has no status command doctor knows, and reads as skipped.
var harnessStatusCommands = map[string][]string{
	"claude": {"auth", "status"},
}

// roleProgram is the first word of a role's runtime command, by base name.
func roleProgram(command string) string {
	f := strings.Fields(command)
	if len(f) == 0 {
		return ""
	}
	return filepath.Base(f[0])
}

func checkLogins(r *doctorReport, in doctorInput) {
	if in.Manifest == nil {
		r.line(statSkip, "login", "no manifest named; no harness to ask", "")
		return
	}
	seen := map[string]bool{}
	for _, t := range in.Manifest.Teams {
		for _, role := range t.Roles {
			prog := roleProgram(role.Runtime.Command)
			if prog == "" || seen[prog] {
				continue
			}
			seen[prog] = true
			args, ok := harnessStatusCommands[prog]
			if !ok {
				r.line(statSkip, "login "+prog, "no status command known for this harness", "")
				continue
			}
			if _, err := in.LookPath(prog); err != nil {
				continue // the harness stage already failed on it
			}
			if _, err := in.Run(prog, args...); err != nil {
				r.line(statFail, "login "+prog, "not logged in, or its status command failed", "log in with `"+prog+" auth login`")
				continue
			}
			r.line(statPass, "login "+prog, "logged in", "")
		}
	}
}

func checkSocket(r *doctorReport, in doctorInput) {
	if in.Socket == "" {
		r.line(statSkip, "socket", "no socket path to check", "")
		return
	}
	if err := paths.CheckSocketPath(in.Socket); err != nil {
		r.line(statFail, "socket", err.Error(), "use a shorter marvel home, or set MARVEL_HOME to a short path")
		return
	}
	r.line(statPass, "socket", fmt.Sprintf("%d bytes, under the %d-byte limit", len(in.Socket), paths.MaxUnixSocketPath), "")
}

// managedBus reports whether any cluster declares a managed message bus.
func managedBus(clusters []config.Cluster) bool {
	for i := range clusters {
		svc, err := clusters[i].BusService()
		if err != nil || svc == nil {
			continue
		}
		if svc.Mode == string(service.ModeManaged) {
			return true
		}
	}
	return false
}

func checkNats(r *doctorReport, in doctorInput) {
	if !managedBus(in.Clusters) {
		r.line(statSkip, "bus", "no cluster declares a managed bus", "")
		return
	}
	fix := "brew install nats-server (version " + natsServerMinVersion + " or newer)"
	if _, err := in.LookPath("nats-server"); err != nil {
		r.line(statFail, "bus", "nats-server not found on PATH; a managed bus needs "+natsServerMinVersion+" or newer", fix)
		return
	}
	out, err := in.Run("nats-server", "--version")
	if err != nil {
		r.line(statFail, "bus", "nats-server found, but `--version` did not answer", fix)
		return
	}
	fields := strings.Fields(out)
	got := ""
	if len(fields) > 0 {
		got = strings.TrimPrefix(fields[len(fields)-1], "v")
	}
	if !versionAtLeast(got, natsServerMinVersion) {
		r.line(statFail, "bus", fmt.Sprintf("nats-server %q is older than the %s minimum", got, natsServerMinVersion), fix)
		return
	}
	r.line(statPass, "bus", "nats-server "+got, "")
}

// versionAtLeast compares dotted numeric versions; an unreadable one is not at
// least anything.
func versionAtLeast(got, min string) bool {
	g, ok1 := versionParts(got)
	m, ok2 := versionParts(min)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if g[i] != m[i] {
			return g[i] > m[i]
		}
	}
	return true
}

func versionParts(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// roleUsesShim reports whether a role names the director shim: a DIRECTOR_ key
// in its env, or the shim or the channel flag in its command or args. Only key
// names are read; no env value is looked at or printed.
func roleUsesShim(role api.ManifestRole) bool {
	for k := range role.Runtime.Env {
		if strings.HasPrefix(k, "DIRECTOR_") {
			return true
		}
	}
	text := role.Runtime.Command + " " + strings.Join(role.Runtime.Args, " ")
	return strings.Contains(text, "director-mcp") || strings.Contains(text, "development-channels")
}

func checkShim(r *doctorReport, in doctorInput) {
	used := false
	if in.Manifest != nil {
		for _, t := range in.Manifest.Teams {
			for _, role := range t.Roles {
				if roleUsesShim(role) {
					used = true
				}
			}
		}
	}
	if !used {
		r.line(statSkip, "shim", "no role uses the director shim", "")
		return
	}
	if _, err := in.LookPath("director-mcp"); err != nil {
		r.line(statFail, "shim", "a role uses the director shim and director-mcp is not on PATH", "install director-mcp from the director release")
		return
	}
	r.line(statPass, "shim", "director-mcp found", "")
}

func checkKeys(r *doctorReport, in doctorInput) {
	if _, err := os.Stat(in.Home.Home); err != nil {
		r.line(statSkip, "keys", "no marvel home yet; nothing to audit", "")
		return
	}
	issues, err := in.Home.Audit()
	if err != nil {
		r.line(statFail, "keys", "could not audit "+in.Home.Home+": "+err.Error(), "")
		return
	}
	if len(issues) == 0 {
		r.line(statPass, "keys", "permissions under the marvel home are as expected", "")
		return
	}
	r.line(statFail, "keys", fmt.Sprintf("%d permission issue(s) under the marvel home", len(issues)), "run `marvel keys doctor --fix`")
}

func checkSeats(r *doctorReport, in doctorInput) {
	if in.Sessions == nil {
		r.line(statSkip, "seats", "no daemon read attempted", "")
		return
	}
	sessions, err := in.Sessions()
	if err != nil {
		r.line(statSkip, "seats", "the daemon did not answer; no live seat was read", "")
		return
	}
	if len(sessions) == 0 {
		r.line(statSkip, "seats", "no live seats", "")
		return
	}
	for _, s := range sessions {
		name := "seat " + s.Name
		switch s.Pane {
		case "prompt":
			r.line(statWait, name, "waits at a menu; text typed or injected will not reach the model until someone answers it (marvel attach)", "")
		case "logged-out":
			r.line(statFail, name, "its harness is logged out", "attach to the seat and log in (marvel attach "+s.Name+")")
		case "fresh":
			r.line(statPass, name, "fresh; it has not taken a turn yet", "")
		case "":
			r.line(statSkip, name, "pane state not read", "")
		default:
			r.line(statPass, name, s.Pane, "")
		}
	}
}

// doctorCmd is marvel doctor: a read-only first-run check. It writes nothing and
// changes no setting; its exit code is for the user's own scripts.
func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor [manifest]",
		Short: "Check this host for what a first run needs",
		Long: `Check this host for what a first run needs, and say what is missing.

doctor prints one line per check, and a fix line under each failure. A stage
with nothing configured reads SKIP, not FAIL. Name a manifest to check its
harness commands, their logins and the director shim.

doctor is read-only: it writes nothing and changes no setting. It never reads
a credential file; a harness login is asked of the harness's own status
command. It exits nonzero when a check failed, for your own scripts; no marvel
command reads that code.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := hostDoctorInput(args)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return &exitError{Code: 1, Msg: err.Error()}
			}
			return doctorExit(runDoctor(cmd.OutOrStdout(), in))
		},
	}
}

// hostDoctorInput fills the input from the real host.
func hostDoctorInput(args []string) (doctorInput, error) {
	layout, err := paths.Default()
	if err != nil {
		return doctorInput{}, err
	}
	in := doctorInput{
		LookPath: exec.LookPath,
		Run: func(name string, a ...string) (string, error) {
			out, err := exec.Command(name, a...).CombinedOutput()
			return string(out), err
		},
		Home:   layout,
		Socket: layout.RuntimeSocket(),
	}
	if addr, _, derr := resolveDaemonAddr(); derr == nil && addr != "" {
		in.Socket = addr
	}
	if len(args) == 1 {
		m, err := api.ParseManifest(args[0])
		if err != nil {
			return doctorInput{}, fmt.Errorf("read manifest %s: %w", args[0], err)
		}
		in.Manifest = m
	}
	if cfg, err := config.Load(); err == nil {
		in.Clusters = cfg.Clusters
	}
	in.Sessions = func() ([]api.Session, error) {
		params, _ := json.Marshal(map[string]any{"resource_type": "sessions", "composer": true})
		resp, err := send(daemonrpc.Request{Method: "get", Params: params})
		if err != nil {
			return nil, err
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("%s", resp.Error)
		}
		var out []api.Session
		if err := json.Unmarshal(resp.Result, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	return in, nil
}
