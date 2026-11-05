package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// errRemoteNotDialed is what the seat read returns for a daemon that is not a
// local unix socket and was not asked for with --remote. It is a skip, not a
// failure, and nothing was dialed.
var errRemoteNotDialed = errors.New("remote cluster, not dialed")

// errKnownHostsAbsent is what the seat read returns for a --remote read when the
// host-key file does not exist. Dialing would create an empty file under the
// marvel home (knownhosts.Callback), and doctor writes nothing, so it stops
// before the dial. It is a skip, not a failure.
var errKnownHostsAbsent = errors.New("known_hosts absent")

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
	// Sessions reads the live seats with their PANE word. It returns
	// errRemoteNotDialed for a remote daemon that was not asked for, and any
	// other error when the daemon does not answer. Nil means no read.
	Sessions func() ([]api.Session, error)
}

const (
	statPass = "PASS"
	statFail = "FAIL"
	statSkip = "SKIP"
	statWait = "WAIT"
	statWarn = "WARN"
)

// tmuxMinVersion is the oldest tmux marvel is measured against. Below it tmux
// can lose a dead pane's exit status (marvel#424), so a headless run that
// exited 0 is stored as crashed.
var tmuxMinVersion = [2]int{3, 5}

// doctorReport collects the lines and counts the failures.
type doctorReport struct {
	w      io.Writer
	failed int
}

// line writes one check line, and a fix line under a failure. Callers pass fixed
// text and values doctor itself parsed; no line carries manifest command text or
// an env value.
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

// firstLine is the first line of a program's output, cut to a short printable
// run, so a program that prints something unexpected cannot flood the report.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	var b strings.Builder
	for _, r := range s {
		if r < ' ' || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 60 {
			break
		}
	}
	return b.String()
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
	line := firstLine(out)
	got, ok := tmuxVersion(tmuxVersionField(line))
	switch {
	case !ok:
		r.line(statWarn, "tmux", line+" (version not read; marvel needs 3.5 or later, marvel#424)", "")
	case got[0] < tmuxMinVersion[0] || (got[0] == tmuxMinVersion[0] && got[1] < tmuxMinVersion[1]):
		r.line(statWarn, "tmux", line+" (below 3.5; it can lose a pane's exit status, so a run that exits 0 reads as crashed, marvel#424)", "")
	default:
		r.line(statPass, "tmux", line, "")
	}
}

// tmuxVersionField is the word after "tmux" in `tmux -V` output, or "".
func tmuxVersionField(line string) string {
	f := strings.Fields(line)
	if len(f) < 2 || f[0] != "tmux" {
		return ""
	}
	return f[1]
}

// tmuxVersion reads major.minor from a tmux version word: "3.5a" and "3.4" as
// they are, "next-3.6" as 3.6, the release a development build leads up to. A
// word with no major.minor ("master", "next", "3") is unreadable.
func tmuxVersion(v string) ([2]int, bool) {
	var out [2]int
	v = strings.TrimPrefix(v, "next-")
	major, rest, ok := strings.Cut(v, ".")
	if !ok {
		return out, false
	}
	minor := rest
	if i := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		minor = rest[:i]
	}
	a, err1 := strconv.Atoi(major)
	b, err2 := strconv.Atoi(minor)
	if err1 != nil || err2 != nil || a < 0 || b < 0 {
		return out, false
	}
	return [2]int{a, b}, true
}

// checkRuntimes reuses ValidateRuntimes and prints the role it names, never the
// command: a command can carry a token as its first word, as an assignment or
// as a flag value, and no way of cutting it down is safe for every shape.
func checkRuntimes(r *doctorReport, in doctorInput) {
	if in.Manifest == nil {
		r.line(statSkip, "harness", "no manifest named; pass one to check its harness commands", "")
		return
	}
	advisories, err := in.Manifest.ValidateRuntimes()
	if err != nil {
		r.line(statFail, "harness", "a role's program does not resolve", "install the program the role's runtime.command starts, or correct that command")
		lines := strings.Split(err.Error(), "\n")
		for _, l := range lines[1:] {
			_, _ = fmt.Fprintf(r.w, "      %s\n", roleOnly(strings.TrimSpace(l)))
		}
		return
	}
	r.line(statPass, "harness", "every role's command resolves", "")
	for _, a := range advisories {
		_, _ = fmt.Fprintf(r.w, "      note: %s\n", a)
	}
}

// roleOnly keeps the "team[i=name].role[j=name]" prefix of a ValidateRuntimes
// error line and says only whether the command or the script did not resolve.
func roleOnly(line string) string {
	for _, kind := range []string{"command", "script"} {
		if i := strings.Index(line, ": "+kind+" "); i >= 0 {
			return line[:i] + ": " + kind + " does not resolve"
		}
	}
	return "a role's program does not resolve"
}

// harnessStatusCommands maps a harness program to its own login status command.
// doctor asks only that command and never reads a credential file. A harness
// absent from the map has no status command doctor knows, and reads as skipped.
var harnessStatusCommands = map[string][]string{
	"claude": {"auth", "status"},
}

// harnessOf reads the program a role's command starts, past `env`, its flags and
// leading NAME=value assignments, and returns its path as written and its base
// name. It reports ok=false when no program can be read. It returns the path so
// the caller can ask that program, not a different one on PATH, and it never
// returns an assignment, which can hold a token.
func harnessOf(command string) (path, base string, ok bool) {
	fields := strings.Fields(command)
	for len(fields) > 0 {
		f := fields[0]
		switch {
		case strings.Contains(f, "="):
			fields = fields[1:]
		case filepath.Base(f) == "env":
			fields = fields[1:]
			for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
				// -u and --unset take the variable's name as the next field,
				// which is not the program.
				takesName := fields[0] == "-u" || fields[0] == "--unset"
				fields = fields[1:]
				if takesName && len(fields) > 0 {
					fields = fields[1:]
				}
			}
		default:
			return f, filepath.Base(f), true
		}
	}
	return "", "", false
}

func checkLogins(r *doctorReport, in doctorInput) {
	if in.Manifest == nil {
		r.line(statSkip, "login", "no manifest named; no harness to ask", "")
		return
	}
	asked := map[string]bool{}
	other := 0
	for _, t := range in.Manifest.Teams {
		for _, role := range t.Roles {
			path, base, ok := harnessOf(role.Runtime.Command)
			args, known := harnessStatusCommands[base]
			if !ok || !known {
				other++
				continue
			}
			if asked[path] {
				continue
			}
			asked[path] = true
			name := "login " + base
			prog := path
			if !filepath.IsAbs(path) {
				found, err := in.LookPath(path)
				if err != nil {
					r.line(statSkip, name, "not found on PATH, so its login was not asked", "")
					continue
				}
				prog = found
			}
			if _, err := in.Run(prog, args...); err != nil {
				r.line(statFail, name, "not logged in, or its status command failed", "log in with `"+base+" auth login`")
				continue
			}
			r.line(statPass, name, "logged in", "")
		}
	}
	if other > 0 {
		r.line(statSkip, "login", fmt.Sprintf("%d role(s) start a harness with no known status command", other), "")
	}
}

func checkSocket(r *doctorReport, in doctorInput) {
	if in.Socket == "" {
		r.line(statSkip, "socket", "no socket path to check", "")
		return
	}
	if paths.IsTCPAddr(in.Socket) {
		r.line(statSkip, "socket", "the daemon address is not a unix socket; no length limit applies", "")
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
	if _, ok := versionParts(got); !ok {
		r.line(statFail, "bus", "nats-server answered, but its version could not be read", fix)
		return
	}
	if !versionAtLeast(got, natsServerMinVersion) {
		r.line(statFail, "bus", fmt.Sprintf("nats-server %s is older than the %s minimum", got, natsServerMinVersion), fix)
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

// versionParts reads major.minor[.patch], past a prerelease or build suffix
// ("2.15.0-RC.1" is read as 2.15.0, the release it leads up to).
func versionParts(v string) ([3]int, bool) {
	var out [3]int
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
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
		r.line(statFail, "keys", "could not audit the marvel home", "check that the home is readable by you")
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
	if errors.Is(err, errRemoteNotDialed) {
		r.line(statSkip, "seats", "remote cluster, not dialed; pass --remote to read its seats", "")
		return
	}
	if errors.Is(err, errKnownHostsAbsent) {
		r.line(statSkip, "seats", "known_hosts absent; remote host key check skipped; run one interactive remote command to trust the host", "")
		return
	}
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
		case "working", "idle":
			r.line(statPass, name, s.Pane, "")
		case "restart":
			r.line(statWait, name, "restarting", "")
		default:
			r.line(statSkip, name, "pane state not read", "")
		}
	}
}

// doctorCmd is marvel doctor: a read-only first-run check. It writes nothing and
// changes no setting; its exit code is for the user's own scripts.
func doctorCmd() *cobra.Command {
	var remote bool
	cmd := &cobra.Command{
		Use:   "doctor [manifest]",
		Short: "Check this host for what a first run needs",
		Long: `Check this host for what a first run needs, and say what is missing.

doctor prints one line per check, and a fix line under each failure. A stage
with nothing configured reads SKIP, not FAIL. Name a manifest to check its
harness commands, their logins and the director shim.

doctor is read-only: it writes nothing and changes no setting. It never reads
a credential file; a harness login is asked of the harness's own status
command. It prints no command text and no env value from a manifest.

The live seats are read only from a daemon on a local unix socket. For a remote
cluster (an mrvl:// or ssh:// address) the seats read SKIP and nothing is
dialed. --remote asks for that read: it loads the cluster's identity key, dials
the host, and may offer an unknown host key for trust, as any marvel command
that talks to a remote cluster does. With no known_hosts file yet, doctor skips
the read and creates nothing: run one interactive remote command to trust the
host first.

doctor exits nonzero when a check failed, for your own scripts. No marvel
command reads that code.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := hostDoctorInput(args, remote)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return &exitError{Code: 1, Msg: err.Error()}
			}
			return doctorExit(runDoctor(cmd.OutOrStdout(), in))
		},
	}
	cmd.Flags().BoolVar(&remote, "remote", false, "also read the live seats of a remote cluster (dials it and loads the identity key)")
	return cmd
}

// isRemoteAddr reports whether a daemon address is anything but a local unix
// socket: a host:port or an mrvl:// or ssh:// address.
func isRemoteAddr(addr string) bool {
	return paths.IsTCPAddr(addr) || strings.Contains(addr, "://")
}

// manifestReadError says why a manifest could not be read without quoting what
// the parser quoted back, which can be a line of the file and so a value.
func manifestReadError(path string, err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("read manifest %s: %w", path, pe.Err)
	}
	return fmt.Errorf("manifest %s is not a valid manifest", path)
}

// hostDoctorInput fills the input from the real host. The seat read dials only a
// local unix socket unless remote is set.
func hostDoctorInput(args []string, remote bool) (doctorInput, error) {
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
	addr := ""
	if a, _, derr := resolveDaemonAddr(); derr == nil && a != "" {
		addr = a
		in.Socket = a
	}
	if len(args) == 1 {
		m, err := api.ParseManifest(args[0])
		if err != nil {
			return doctorInput{}, manifestReadError(args[0], err)
		}
		in.Manifest = m
	}
	if cfg, err := config.Load(); err == nil {
		in.Clusters = cfg.Clusters
	}
	in.Sessions = func() ([]api.Session, error) {
		if isRemoteAddr(addr) && !remote {
			return nil, errRemoteNotDialed
		}
		// The dial would create an empty known_hosts; doctor creates nothing.
		if isRemoteAddr(addr) {
			if _, err := os.Stat(layout.KnownHosts()); errors.Is(err, os.ErrNotExist) {
				return nil, errKnownHostsAbsent
			}
		}
		params, _ := json.Marshal(map[string]any{"resource_type": "sessions", "composer": true})
		resp, err := send(daemonrpc.Request{Method: "get", Params: params})
		if err != nil {
			return nil, err
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("daemon refused the read")
		}
		var out []api.Session
		if err := json.Unmarshal(resp.Result, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	return in, nil
}
