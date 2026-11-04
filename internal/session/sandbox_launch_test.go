package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

// The launch half of a sandbox-exec profile (sandbox_launch.go). No model and no
// real sandbox-exec here: a fake stands in, and the tests that run the real one
// are darwin-tagged (sandbox_launch_darwin_test.go).

type sandboxRig struct {
	store   *api.Store
	mgr     *Manager
	ring    *events.Ring
	profile string // the role's profile, as the manifest names it
	exec    string // the fake sandbox-exec
	harness string // the role's command
	work    string
	home    string // the seat's harness home
	copies  string // marvel's per-launch copy directory
}

func newSandboxRig(t *testing.T, role api.Role, placement api.Placement) *sandboxRig {
	t.Helper()
	store := api.NewStore()
	mgr := NewManager(store, nil)
	ring := events.NewRing(64)
	mgr.Events = ring
	mgr.GOOS = "darwin"
	mgr.HarnessHomeDir = t.TempDir()
	if err := os.Chmod(mgr.HarnessHomeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mgr.RealHome = t.TempDir()
	mgr.SandboxDir = t.TempDir()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	execPath := filepath.Join(dir, "sandbox-exec")
	writeFile(t, execPath, "#!/bin/sh\nexit 0\n", 0o755)
	mgr.SandboxExec = execPath
	profile := filepath.Join(dir, "p.sb")
	writeFile(t, profile, "(version 1)\n(deny default)\n", 0o644)
	harness := filepath.Join(dir, "harness")
	writeFile(t, harness, "#!/bin/sh\nexec sleep 300\n", 0o755)
	work, _ := filepath.EvalSymlinks(t.TempDir())
	home, _ := filepath.EvalSymlinks(t.TempDir())
	role.Name = "seat"
	role.Runtime.Command = harness
	role.SandboxProfile = profile
	if err := store.CreateWorkspace(&api.Workspace{Name: "w", Placement: placement, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTeam(&api.Team{
		Name: "squad", Workspace: "w", Generation: 1, Roles: []api.Role{role},
		ConvergencePosture: api.PostureConverge, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return &sandboxRig{store: store, mgr: mgr, ring: ring, profile: profile, exec: execPath, harness: harness, work: work, home: home, copies: mgr.SandboxDir}
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func (r *sandboxRig) session() *api.Session {
	return &api.Session{
		Name: "seat-g1-0", Workspace: "w", Team: "squad", Role: "seat", Generation: 1,
		WorkDir: r.work, HarnessHome: r.home,
		Runtime: api.Runtime{Name: "sandboxtest", Command: r.harness},
	}
}

func (r *sandboxRig) refusedEvents() []events.Event {
	return r.ring.Snapshot(events.Filter{Kind: events.KindSessionSandboxRefused}, 0)
}

// Each refusal: the spawn is refused with its own reason under the #sandbox key,
// one event per change, and the record ends on its own once the cause is fixed.
func TestSandboxRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		break_ func(r *sandboxRig, role *api.Role)
		fix    func(r *sandboxRig)
		reason string
	}{
		{"not darwin", func(r *sandboxRig, _ *api.Role) { r.mgr.GOOS = "linux" }, func(r *sandboxRig) { r.mgr.GOOS = "darwin" }, RefusalSandboxHost},
		{"no sandbox-exec", func(r *sandboxRig, _ *api.Role) { r.mgr.SandboxExec = filepath.Join(r.copies, "absent") }, func(r *sandboxRig) { r.mgr.SandboxExec = r.exec }, RefusalSandboxHost},
		{"profile missing", func(r *sandboxRig, _ *api.Role) { _ = os.Remove(r.profile) }, func(r *sandboxRig) { writeFile(t, r.profile, "(version 1)\n", 0o644) }, RefusalSandboxProfile},
		{"profile is a directory", func(r *sandboxRig, _ *api.Role) {
			_ = os.Remove(r.profile)
			_ = os.Mkdir(r.profile, 0o755)
		}, func(r *sandboxRig) {
			_ = os.Remove(r.profile)
			writeFile(t, r.profile, "(version 1)\n", 0o644)
		}, RefusalSandboxProfile},
		{"harness not found", nil, func(r *sandboxRig) {}, RefusalSandboxHarness},
		{"command holds an operator", nil, func(r *sandboxRig) {}, RefusalSandboxShape},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
			sess := r.session()
			switch tc.name {
			case "harness not found":
				sess.Runtime.Command = "no-such-harness-anywhere"
				r.mgr.LookPath = func(string) (string, error) { return "", errors.New("not found") }
				tc.fix = func(*sandboxRig) { sess.Runtime.Command = r.harness }
			case "command holds an operator":
				sess.Runtime.Command = r.harness + "; rm -rf /"
				tc.fix = func(*sandboxRig) { sess.Runtime.Command = r.harness }
			default:
				tc.break_(r, nil)
			}
			err := r.mgr.checkSandbox(sess)
			if !errors.Is(err, ErrPlacementRefused) {
				t.Fatalf("checkSandbox = %v, want a placement refusal", err)
			}
			rec := r.mgr.PlacementRefusals()
			if len(rec) != 1 || rec[0].Reason != tc.reason {
				t.Fatalf("refusals = %+v, want one with reason %q", rec, tc.reason)
			}
			_ = r.mgr.checkSandbox(sess)
			if got := r.refusedEvents(); len(got) != 1 {
				t.Fatalf("%d sandbox-refused events after two attempts, want 1", len(got))
			}
			tc.fix(r)
			if err := r.mgr.checkSandbox(sess); err != nil {
				t.Fatalf("after the fix: %v", err)
			}
			if rec := r.mgr.PlacementRefusals(); len(rec) != 0 {
				t.Errorf("refusal stands after the fix: %+v", rec)
			}
		})
	}
}

// A profile and a container placement are refused together, with their own
// reason, and nothing is started on the host.
func TestSandboxWithContainerIsRefused(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{ContainerImage: "img:1"}, api.PlacementContainer)
	err := r.mgr.checkSandbox(r.session())
	if !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("checkSandbox = %v", err)
	}
	if rec := r.mgr.PlacementRefusals(); len(rec) != 1 || rec[0].Reason != RefusalSandboxContainer {
		t.Errorf("refusals = %+v", rec)
	}
}

// The #sandbox key is its own: it neither clears nor is cleared by the container
// refusal of the same role.
func TestSandboxRefusalKeyIsItsOwn(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	r.mgr.GOOS = "linux"
	sess := r.session()
	_ = r.mgr.checkSandbox(sess)
	r.mgr.refusedMu.Lock()
	_, ok := r.mgr.refused["w/squad/seat"+sandboxRefusalSuffix]
	r.mgr.refusedMu.Unlock()
	if !ok || sandboxRefusalSuffix != "#sandbox" {
		t.Errorf("refusal not under the #sandbox key (suffix %q)", sandboxRefusalSuffix)
	}
	// A role with no profile has no standing sandbox record.
	roleNoProfile := api.Role{Name: "plain", Runtime: api.Runtime{Command: r.harness}}
	_ = r.store.CreateTeam(&api.Team{Name: "t2", Workspace: "w", Generation: 1, Roles: []api.Role{roleNoProfile}, ConvergencePosture: api.PostureConverge, CreatedAt: time.Now().UTC()})
	plain := &api.Session{Name: "plain-g1-0", Workspace: "w", Team: "t2", Role: "plain", Runtime: api.Runtime{Command: r.harness}}
	if err := r.mgr.checkSandbox(plain); err != nil {
		t.Errorf("a role with no profile was refused: %v", err)
	}
}

// A missing marvel binary is refused before launch: the goose refusal stub that
// would otherwise run is /bin/sh, which a profile kills.
func TestSandboxRefusesAMissingMarvelBinary(t *testing.T) {
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	old := sandboxExecutable
	sandboxExecutable = func() (string, error) { return "", errors.New("no executable") }
	t.Cleanup(func() { sandboxExecutable = old })
	err := r.mgr.checkSandbox(r.session())
	if !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("checkSandbox = %v", err)
	}
	if rec := r.mgr.PlacementRefusals(); len(rec) != 1 || rec[0].Reason != RefusalSandboxHarness {
		t.Errorf("refusals = %+v", rec)
	}
}

func TestResolveSandboxResolvesBothBinaries(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	// The role's command is reached through a symlink: the exec rule matches the
	// resolved file.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(r.harness, link); err != nil {
		t.Fatal(err)
	}
	sess := r.session()
	sess.Runtime.Command = link
	launch, fail := r.mgr.resolveSandbox(sess)
	if fail != nil || launch == nil {
		t.Fatalf("resolveSandbox = %v, %v", launch, fail)
	}
	if want, _ := filepath.EvalSymlinks(r.harness); launch.harnessBin != want {
		t.Errorf("harnessBin = %q, want %q", launch.harnessBin, want)
	}
	self, _ := os.Executable()
	if want, _ := filepath.EvalSymlinks(self); launch.marvelBin != want {
		t.Errorf("marvelBin = %q, want %q", launch.marvelBin, want)
	}
}

// planLaunch stages the profile: a per-launch copy in marvel's own directory,
// outside the seat's writable trees, with the hash of the copy it holds.
func TestPlanLaunchStagesTheProfileCopy(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	sess := r.session()
	plan := r.mgr.planLaunch(sess)
	if plan.sandboxFail != nil || plan.sandbox == nil {
		t.Fatalf("plan = %+v", plan)
	}
	sb := plan.sandbox
	if filepath.Dir(sb.copyPath) != r.copies {
		t.Errorf("copy %q is not in %q", sb.copyPath, r.copies)
	}
	got, err := os.ReadFile(sb.copyPath)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(r.profile)
	if string(got) != string(src) {
		t.Errorf("the copy differs from the profile")
	}
	sum := sha256.Sum256(got)
	if sb.sha != hex.EncodeToString(sum[:]) {
		t.Errorf("sha = %s, want the hash of the copy", sb.sha)
	}
	if info, _ := os.Stat(sb.copyPath); info.Mode().Perm() != 0o600 {
		t.Errorf("copy mode = %v, want 0600", info.Mode().Perm())
	}
	// Editing the source afterwards changes nothing about what is in force.
	writeFile(t, r.profile, "(version 1)\n(allow default)\n", 0o644)
	again, _ := os.ReadFile(sb.copyPath)
	if string(again) != string(src) {
		t.Error("the copy followed an edit of the source")
	}
	// A second launch of the same session replaces the first copy.
	plan2 := r.mgr.planLaunch(sess)
	if plan2.sandbox == nil || plan2.sandbox.copyPath == sb.copyPath {
		t.Fatalf("second plan = %+v", plan2)
	}
	if _, err := os.Stat(sb.copyPath); err == nil {
		t.Error("the first launch's copy was kept")
	}
}

// If the profile vanishes between the refusal check and planLaunch, the launch is
// held, never started unsandboxed.
func TestPlanLaunchHoldsWhenTheProfileVanishes(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	_ = os.Remove(r.profile)
	plan := r.mgr.planLaunch(r.session())
	if plan.sandboxFail == nil || plan.sandboxFail.reason != RefusalSandboxProfile || plan.command != "" {
		t.Fatalf("plan = %+v", plan)
	}
}

func goosePlanRole(profile string) api.Role {
	return api.Role{
		Name:           "seat",
		SandboxProfile: profile,
		Runtime: api.Runtime{
			Name: "goose", Command: "goose", Mode: api.RuntimeModeHeadless, Prompt: "say ok",
			Goose: &api.GooseRuntime{Posture: api.GoosePostureAuto},
		},
	}
}

// A headless auto goose role that names a profile passes the posture rule and
// gets the watched launch; the same role with no profile is refused as
// auto-unsandboxed.
func TestAProfiledGooseRolePassesThePostureRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		profile bool
		want    string
	}{{"with a profile", true, "'goose-watch'"}, {"without one", false, "'goose-refuse' 'auto-unsandboxed'"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
			role := goosePlanRole(r.profile)
			if !tc.profile {
				role.SandboxProfile = ""
			}
			if err := r.store.UpdateTeam("w/squad", func(t *api.Team) error { t.Roles = []api.Role{role}; return nil }); err != nil {
				t.Fatal(err)
			}
			// goose is looked up by the harness, which the fake provides on PATH.
			bin := filepath.Join(t.TempDir(), "goose")
			writeFile(t, bin, "#!/bin/sh\nexit 0\n", 0o755)
			r.mgr.LookPath = func(string) (string, error) { return bin, nil }
			sess := r.session()
			sess.HarnessHome = ""
			sess.Runtime = role.Runtime
			plan := r.mgr.planLaunch(sess)
			if plan.sandboxFail != nil || plan.homeRefusal != "" {
				t.Fatalf("plan = %+v", plan)
			}
			if !strings.Contains(plan.command, tc.want) {
				t.Errorf("command %q does not contain %q", plan.command, tc.want)
			}
			if tc.profile && plan.sandbox == nil {
				t.Error("no staged profile")
			}
		})
	}
}

// A profiled role whose adapter fails to prepare the launch is refused and never
// given directPlan's raw command, which would drop the rules the adapter applies
// (for goose, the watchdog and the posture refusal). An unprofiled role keeps the
// fallback. A headless claude with no prompt is the forced Prepare error.
func TestAProfiledRoleWithAPrepareErrorIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		profile bool
	}{{"profiled", true}, {"unprofiled", false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
			role := api.Role{
				Name:           "seat",
				SandboxProfile: r.profile,
				Runtime:        api.Runtime{Name: "claude", Command: r.harness, Mode: api.RuntimeModeHeadless},
			}
			if !tc.profile {
				role.SandboxProfile = ""
			}
			if err := r.store.UpdateTeam("w/squad", func(t *api.Team) error { t.Roles = []api.Role{role}; return nil }); err != nil {
				t.Fatal(err)
			}
			sess := r.session()
			sess.Runtime = role.Runtime
			plan := r.mgr.planLaunch(sess)
			if tc.profile {
				if plan.sandboxFail == nil || plan.sandboxFail.reason != RefusalSandboxPrepare || plan.command != "" {
					t.Fatalf("plan = %+v, want a sandbox prepare refusal and no command", plan)
				}
				if !strings.Contains(plan.sandboxFail.msg, "claude") || !strings.Contains(plan.sandboxFail.msg, "no prompt") {
					t.Errorf("message %q does not carry the adapter and the Prepare error", plan.sandboxFail.msg)
				}
				if _, err := os.Stat(r.copies); err == nil {
					if left, _ := filepath.Glob(filepath.Join(r.copies, "*.sb")); len(left) != 0 {
						t.Errorf("a staged copy was left behind: %v", left)
					}
				}
				return
			}
			if plan.sandboxFail != nil || plan.command == "" {
				t.Fatalf("an unprofiled role lost its fallback: %+v", plan)
			}
		})
	}
}

// The prefix goes in front of the planned command text, so the pane shell opens
// the command's own redirections outside the sandbox. Five fixed parameters, in
// order, each quoted.
func TestWrapInSandboxPrefixesAndLeavesRedirectionsOutside(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	sess := r.session()
	plan := r.mgr.planLaunch(sess)
	if plan.sandbox == nil {
		t.Fatalf("plan = %+v", plan)
	}
	orig := "'/m/marvel' 'goose-watch' '--' 'goose' 'run' < '/dev/null' > '/run/sink'"
	plan.command = orig
	if fail := r.mgr.wrapInSandbox(sess, &plan); fail != nil {
		t.Fatal(fail.msg)
	}
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	harness, _ := filepath.EvalSymlinks(r.harness)
	wantPrefix := shellQuote(r.exec) + " -f " + shellQuote(plan.sandbox.copyPath) +
		" -D " + shellQuote("HARNESS_BIN="+harness) +
		" -D " + shellQuote("MARVEL_BIN="+self) +
		" -D " + shellQuote("REALHOME="+r.mgr.RealHome) +
		" -D " + shellQuote("SESSION_DIR="+r.home) +
		" -D " + shellQuote("WORKDIR="+r.work)
	if plan.command != wantPrefix+" "+orig {
		t.Errorf("command:\n got %s\nwant %s", plan.command, wantPrefix+" "+orig)
	}
	prefix := strings.TrimSuffix(plan.command, " "+orig)
	if strings.ContainsAny(strings.NewReplacer("'", "").Replace(prefix), "<>") {
		t.Errorf("the prefix carries a redirection: %s", prefix)
	}
	if strings.Contains(prefix, "/dev/null") || strings.Contains(prefix, "/run/sink") {
		t.Errorf("a redirection target sits inside the prefix: %s", prefix)
	}
	if shapeIssue(prefix, true) != "" {
		t.Errorf("the prefix is not one simple command: %s", prefix)
	}
}

// Every branch is wrapped, and a command that is not one simple command is
// refused with its own reason rather than half-wrapped.
func TestWrapInSandboxRefusesWhatItCannotPrefix(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	sess := r.session()
	plan := r.mgr.planLaunch(sess)
	for _, cmd := range []string{"a; b", "a && b", "a | b", "$(x)", "x\ny", "A=1 cmd", `"a b" c`, "a # b"} {
		p := plan
		p.command = cmd
		fail := r.mgr.wrapInSandbox(sess, &p)
		if fail == nil || fail.reason != RefusalSandboxShape {
			t.Errorf("command %q: fail = %+v, want a shape refusal", cmd, fail)
		}
	}
}

// The profile's copy must not sit in a tree the seat may write, or the seat
// could rewrite what its next launch reads.
func TestWrapInSandboxRefusesACopyInsideAWritableTree(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	sess := r.session()
	r.mgr.SandboxDir = filepath.Join(r.work, "profiles")
	plan := r.mgr.planLaunch(sess)
	if plan.sandbox == nil {
		t.Fatalf("plan = %+v", plan)
	}
	plan.command = "'x'"
	if fail := r.mgr.wrapInSandbox(sess, &plan); fail == nil || fail.reason != RefusalSandboxProfile {
		t.Errorf("fail = %+v", fail)
	}
}

// The one shape function serves both inputs: the raw Runtime.Command (quotes
// false) and the planned command (quotes true). A ; or | inside single quotes is
// data and passes in the second form.
func TestShapeIssue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		text   string
		quotes bool
		issue  bool
	}{
		{"claude", false, false},
		{"claude --flag value", false, false},
		{"'/bin/x' 'a;b' 'c|d' < '/dev/null' > '/s'", true, false},
		{"'it'\\''s' 'ok'", true, false},
		{"a;b", true, true},
		{"'a' ; 'b'", true, true},
		{"'a' | 'b'", true, true},
		{"'a' && 'b'", true, true},
		{"'unterminated", true, true},
		{"x 'q'", false, true},
		{`x \y`, false, true},
		{`x "y"`, true, true},
		{"x\ny", true, true},
		{"x `y`", true, true},
		{"x $(y)", true, true},
		{"A=1 x", true, true},
		{"x #y", true, true},
		{"", true, true},
	} {
		if got := shapeIssue(tc.text, tc.quotes) != ""; got != tc.issue {
			t.Errorf("shapeIssue(%q, %v) issue = %v, want %v (%q)", tc.text, tc.quotes, got, tc.issue, shapeIssue(tc.text, tc.quotes))
		}
	}
}

// The suffix session.created carries is one fixed format, built by one function
// and parsed back by another, and it names marvel's copy, never the source path.
func TestSandboxEventSuffixRoundTrips(t *testing.T) {
	t.Parallel()
	sha := strings.Repeat("ab", 32)
	suffix := sandboxEventSuffix("/run/marvel/sandbox/w-squad-seat-1.sb", sha)
	msg := "pane %1; host memory 3GB" + suffix
	path, got, ok := ParseSandboxEventSuffix(msg)
	if !ok || path != "/run/marvel/sandbox/w-squad-seat-1.sb" || got != sha {
		t.Fatalf("parse = %q %q %v", path, got, ok)
	}
	for _, bad := range []string{"pane %1", "pane %1; sandbox profile /p sha256 " + strings.ToUpper(sha), "pane %1; sandbox profile /p sha256 abc", "pane %1; sandbox profile p sha256 " + sha} {
		if _, _, ok := ParseSandboxEventSuffix(bad); ok {
			t.Errorf("parsed %q", bad)
		}
	}
}

func TestAnnounceSpawnRecordsTheProfileOnlyForAProfiledLaunch(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	sess := r.session()
	sha := strings.Repeat("0a", 32)
	r.mgr.announceSpawn(sess, "%1", runtime.FirstPrompt{}, &sandboxPlan{copyPath: "/c/seat.sb", sha: sha})
	r.mgr.announceSpawn(sess, "%2", runtime.FirstPrompt{}, nil)
	created := r.ring.Snapshot(events.Filter{Kind: events.KindSessionCreated}, 0)
	if len(created) != 2 {
		t.Fatalf("%d session.created events", len(created))
	}
	if p, s, ok := ParseSandboxEventSuffix(created[0].Message); !ok || p != "/c/seat.sb" || s != sha {
		t.Errorf("profiled launch message = %q", created[0].Message)
	}
	if strings.Contains(created[1].Message, "sandbox profile") {
		t.Errorf("an unprofiled launch carries a profile: %q", created[1].Message)
	}
}

// The example profile keeps the order the rulings need: the real home is denied
// before the trees carved back out of it, the bus ports are denied last, and it
// names no host and no user path.
func TestExampleProfileOrderAndContent(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../examples/sandbox/goose-ollama.sb")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	idx := func(s string) int { return strings.Index(text, s) }
	denyHome := idx(`(deny file-read* (subpath (param "REALHOME")))`)
	allowRead := idx(`(allow file-read* (subpath (param "SESSION_DIR")) (subpath (param "WORKDIR")))`)
	allowWrite := idx(`(allow file-write* (subpath (param "SESSION_DIR")) (subpath (param "WORKDIR"))`)
	allowBins := idx(`(allow file-read* (literal (param "HARNESS_BIN")) (literal (param "MARVEL_BIN")))`)
	firstBusDeny := idx(`(deny network-outbound (remote ip "localhost:4222"))`)
	net := idx(`(allow network-outbound (remote ip "localhost:11434"))`)
	for name, i := range map[string]int{"deny home": denyHome, "read trees": allowRead, "write trees": allowWrite, "binaries": allowBins, "bus deny": firstBusDeny, "network": net} {
		if i < 0 {
			t.Fatalf("the profile lacks its %s rule", name)
		}
	}
	if denyHome > allowRead || denyHome > allowWrite || denyHome > allowBins {
		t.Error("the REALHOME deny must come before every allow it carves out")
	}
	if net > firstBusDeny {
		t.Error("the bus-port denies must come last")
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "(version 1)") || !strings.Contains(text, "(deny default)") {
		t.Error("the profile is not deny by default")
	}
	for _, p := range []string{"SESSION_DIR", "WORKDIR", "MARVEL_BIN", "HARNESS_BIN", "REALHOME"} {
		if !strings.Contains(text, `(param "`+p+`")`) {
			t.Errorf("the profile never uses %s", p)
		}
	}
	for _, bad := range []string{"/Users/", "/home/"} {
		if strings.Contains(text, bad) {
			t.Errorf("the profile names a user path (%s)", bad)
		}
	}
}

// Create decides at site 1 whether a seat is profiled and keeps that answer for
// the check after planLaunch. planLaunch's team, role and workspace lookups can
// miss after site 1 passed, and it then returns the bare harness command with
// neither a staged profile nor a failure; a profiled seat must not start from
// that plan (every branch of planLaunch is wrapped, or refused).
func TestAProfiledSeatWhosePlanFallsBackToTheDirectCommandIsRefused(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, r *sandboxRig){
		"workspace missing": func(t *testing.T, r *sandboxRig) {
			if err := r.store.DeleteWorkspace("w"); err != nil {
				t.Fatal(err)
			}
		},
		"team missing": func(t *testing.T, r *sandboxRig) {
			if err := r.store.DeleteTeam("w/squad"); err != nil {
				t.Fatal(err)
			}
		},
		"role missing from its team": func(t *testing.T, r *sandboxRig) {
			team, err := r.store.GetTeam("w/squad")
			if err != nil {
				t.Fatal(err)
			}
			if err := r.store.DeleteTeam("w/squad"); err != nil {
				t.Fatal(err)
			}
			team.Roles[0].Name = "other"
			if err := r.store.CreateTeam(&team); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
			sess := r.session()
			profiled := r.mgr.roleNamesSandbox(sess)
			if !profiled {
				t.Fatal("the role names a profile and roleNamesSandbox says it does not")
			}
			if err := r.mgr.checkSandbox(sess); err != nil {
				t.Fatalf("site 1 = %v, want a pass", err)
			}
			change(t, r)
			plan := r.mgr.planLaunch(sess)
			if plan.sandbox != nil || plan.sandboxFail != nil {
				t.Fatalf("plan = %+v; the premise of this test is a plan with neither", plan)
			}
			fail := r.mgr.unwrappedProfiledLaunch(sess, profiled, plan)
			if fail == nil || fail.reason != RefusalSandboxChanged {
				t.Fatalf("unwrappedProfiledLaunch = %+v, want a %q refusal", fail, RefusalSandboxChanged)
			}
			want := "not started: squad/seat named a sandbox profile when it was checked, and the planned launch carries none (its team, role or workspace changed before the launch was planned); nothing is started unsandboxed"
			if fail.msg != want {
				t.Errorf("message = %q, want %q", fail.msg, want)
			}
		})
	}
}

// A second way to the same plan: planLaunch's own read of the role names the
// profile, and resolveSandbox's read, a moment later, finds none, so it
// returns no launch and no failure and the plan carries neither. The role
// reader is the seam that answers the second read differently.
func TestAProfiledSeatWhoseRoleLosesItsProfileBetweenReadsIsRefused(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	sess := r.session()
	profiled := r.mgr.roleNamesSandbox(sess)
	if !profiled {
		t.Fatal("the role names a profile and roleNamesSandbox says it does not")
	}
	r.mgr.roleReader = func(s *api.Session) (api.Role, bool) {
		team, err := r.store.GetTeam(s.Workspace + "/" + s.Team)
		if err != nil {
			return api.Role{}, false
		}
		for _, role := range team.Roles {
			if role.Name == s.Role {
				role.SandboxProfile = ""
				return role, true
			}
		}
		return api.Role{}, false
	}
	plan := r.mgr.planLaunch(sess)
	if plan.sandbox != nil || plan.sandboxFail != nil || plan.command == "" {
		t.Fatalf("plan = %+v; the premise is a launchable plan with neither a sandbox nor a failure", plan)
	}
	if fail := r.mgr.unwrappedProfiledLaunch(sess, profiled, plan); fail == nil || fail.reason != RefusalSandboxChanged {
		t.Fatalf("unwrappedProfiledLaunch = %+v, want a %q refusal", fail, RefusalSandboxChanged)
	}
}

// A seat whose role never named a profile and takes a direct plan still
// launches: the guard asks only about a seat that was profiled at site 1.
func TestAnUnprofiledSeatOnADirectPlanStillLaunches(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	team, err := r.store.GetTeam("w/squad")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.store.DeleteTeam("w/squad"); err != nil {
		t.Fatal(err)
	}
	team.Roles[0].SandboxProfile = ""
	if err := r.store.CreateTeam(&team); err != nil {
		t.Fatal(err)
	}
	sess := r.session()
	profiled := r.mgr.roleNamesSandbox(sess)
	if profiled {
		t.Fatal("roleNamesSandbox says an unprofiled role is profiled")
	}
	if err := r.store.DeleteWorkspace("w"); err != nil {
		t.Fatal(err)
	}
	plan := r.mgr.planLaunch(sess)
	if plan.command == "" || plan.sandbox != nil || plan.sandboxFail != nil {
		t.Fatalf("plan = %+v, want the direct command", plan)
	}
	if fail := r.mgr.unwrappedProfiledLaunch(sess, profiled, plan); fail != nil {
		t.Errorf("an unprofiled seat was refused: %+v", fail)
	}
}

// A profiled seat whose plan carries its sandbox, or its own failure, is not
// the guard's business.
func TestTheUnwrappedGuardLetsAPlanWithASandboxOrAFailureThrough(t *testing.T) {
	t.Parallel()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	sess := r.session()
	if fail := r.mgr.unwrappedProfiledLaunch(sess, true, launchPlan{sandbox: &sandboxPlan{}}); fail != nil {
		t.Errorf("a profiled seat with its sandbox was refused: %+v", fail)
	}
	if fail := r.mgr.unwrappedProfiledLaunch(sess, true, launchPlan{sandboxFail: &sandboxFailure{reason: RefusalSandboxPrepare}}); fail != nil {
		t.Errorf("a profiled seat with its own failure was double-refused: %+v", fail)
	}
}
