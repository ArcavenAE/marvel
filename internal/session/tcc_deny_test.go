package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The default deny of the macOS privacy folders and network volumes
// (tcc_deny.go, docs/design/seat-tcc-deny.md). No real sandbox-exec: the fake
// from the sandbox rig stands in, and the real profile is run in
// tcc_deny_darwin_test.go.

const tccTestHome = "/Users/seat"

func wantDenied(home string) []string {
	return []string{
		home + "/Desktop", home + "/Documents", home + "/Downloads",
		home + "/Music", home + "/Pictures", home + "/Movies",
		home + "/Library/Mobile Documents", home + "/Library/Calendars",
		home + "/Library/Mail", home + "/Library/Messages", home + "/Library/Safari",
		home + "/Library/Application Support/AddressBook",
		"/Volumes", home + "/OrbStack",
	}
}

func TestTCCDenyBlockNamesEveryFolderAndMount(t *testing.T) {
	t.Parallel()
	block := tccDenyBlock(tccTestHome, []string{"/mnt/share"}, nil)
	if !strings.Contains(block, "(deny file-read* file-write*") {
		t.Fatalf("no deny rule in:\n%s", block)
	}
	for _, p := range append(wantDenied(tccTestHome), "/mnt/share") {
		if !strings.Contains(block, `(subpath "`+p+`")`) {
			t.Errorf("block does not deny %s:\n%s", p, block)
		}
	}
}

// A path that would end the quoted string is dropped, never written, so a mount
// point cannot add a rule of its own.
func TestTCCDenyBlockDropsAPathThatBreaksTheQuoting(t *testing.T) {
	t.Parallel()
	block := tccDenyBlock(tccTestHome, []string{`/mnt/x") (allow default) ("`, "/mnt/back\\slash", "/mnt/ok"}, nil)
	if strings.Contains(block, "allow default") || strings.Contains(block, `back\slash`) {
		t.Errorf("an unsafe mount point reached the profile:\n%s", block)
	}
	if !strings.Contains(block, `(subpath "/mnt/ok")`) {
		t.Errorf("a safe mount point was dropped:\n%s", block)
	}
}

// The seat's own working tree may sit under a denied folder (a user who keeps
// code in ~/Documents). It is allowed back after the deny, so the later rule wins.
func TestTCCDenyBlockAllowsTheWorkdirAfterTheDeny(t *testing.T) {
	t.Parallel()
	work := tccTestHome + "/Documents/code"
	block := tccDenyBlock(tccTestHome, nil, []string{work})
	deny := strings.Index(block, `(subpath "`+tccTestHome+`/Documents")`)
	allow := strings.Index(block, `(allow file-read* file-write* (subpath "`+work+`"))`)
	if deny < 0 || allow < 0 || allow < deny {
		t.Errorf("want the workdir allow after the deny (deny %d, allow %d):\n%s", deny, allow, block)
	}
}

func tccRig(t *testing.T, role api.Role) *sandboxRig {
	t.Helper()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	team, err := r.store.GetTeam("w/squad")
	if err != nil {
		t.Fatal(err)
	}
	role.Name = "seat"
	role.Runtime.Command = r.harness
	if err := r.store.UpdateTeam("w/squad", func(tm *api.Team) error { tm.Roles = []api.Role{role}; return nil }); err != nil {
		t.Fatal(err)
	}
	_ = team
	r.mgr.TCCDeny = true
	r.mgr.NetMounts = func() []string { return []string{"/mnt/share"} }
	return r
}

// A role with no profile and no opt-out gets the deny on its own: no config.
func TestADefaultSeatIsStagedWithTheDenyBlock(t *testing.T) {
	t.Parallel()
	r := tccRig(t, api.Role{})
	sess := r.session()
	sess.WorkDir = filepath.Join(r.mgr.RealHome, "Documents", "code")
	plan := r.mgr.planLaunch(sess)
	if plan.sandboxFail != nil || plan.sandbox == nil {
		t.Fatalf("plan = %+v", plan)
	}
	got, err := os.ReadFile(plan.sandbox.copyPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{"(version 1)", "(allow default)", `(subpath "` + r.mgr.RealHome + `/Pictures")`, `(subpath "/mnt/share")`} {
		if !strings.Contains(text, want) {
			t.Errorf("staged profile lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, `(allow file-read* file-write* (subpath "`+sess.WorkDir+`"))`) {
		t.Errorf("the seat's workdir is not allowed back:\n%s", text)
	}
}

func TestADefaultSeatCommandIsPrefixedWithSandboxExecAndNoParameters(t *testing.T) {
	t.Parallel()
	r := tccRig(t, api.Role{})
	sess := r.session()
	plan := r.mgr.planLaunch(sess)
	orig := "'/h/claude' '--x' < '/dev/null'"
	plan.command = orig
	if fail := r.mgr.wrapInSandbox(sess, &plan); fail != nil {
		t.Fatal(fail.msg)
	}
	want := shellQuote(r.exec) + " -f " + shellQuote(plan.sandbox.copyPath) + " " + orig
	if plan.command != want {
		t.Errorf("command:\n got %s\nwant %s", plan.command, want)
	}
}

// The opt-out, the manager switch and Linux each leave the launch as it was.
func TestTCCDenyIsOffWhereItIsNotWanted(t *testing.T) {
	t.Parallel()
	off := false
	for _, tc := range []struct {
		name string
		set  func(r *sandboxRig)
		role api.Role
	}{
		{"role opts out", func(*sandboxRig) {}, api.Role{TCCDeny: &off}},
		{"manager switch off", func(r *sandboxRig) { r.mgr.TCCDeny = false }, api.Role{}},
		{"linux", func(r *sandboxRig) { r.mgr.GOOS = "linux" }, api.Role{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := tccRig(t, tc.role)
			tc.set(r)
			plan := r.mgr.planLaunch(r.session())
			if plan.sandbox != nil || plan.sandboxFail != nil {
				t.Errorf("plan = %+v, want no sandbox", plan)
			}
		})
	}
}

// A role that names its own profile gets the block appended last, so its own
// allow cannot re-open a folder.
func TestARoleProfileGetsTheBlockLast(t *testing.T) {
	t.Parallel()
	r := tccRig(t, api.Role{})
	writeFile(t, r.profile, "(version 1)\n(allow default)\n(allow file-read* (subpath \""+r.mgr.RealHome+"/Pictures\"))\n", 0o644)
	if err := r.store.UpdateTeam("w/squad", func(tm *api.Team) error { tm.Roles[0].SandboxProfile = r.profile; return nil }); err != nil {
		t.Fatal(err)
	}
	plan := r.mgr.planLaunch(r.session())
	if plan.sandboxFail != nil || plan.sandbox == nil {
		t.Fatalf("plan = %+v", plan)
	}
	got, _ := os.ReadFile(plan.sandbox.copyPath)
	text := string(got)
	allow := strings.Index(text, "(allow file-read* (subpath")
	deny := strings.LastIndex(text, "(deny file-read* file-write*")
	if allow < 0 || deny < allow {
		t.Errorf("want the deny block after the role's own allow (allow %d, deny %d):\n%s", allow, deny, text)
	}
}

// A role with the opt-out keeps its own profile byte for byte.
func TestAnOptedOutRoleProfileIsUnchanged(t *testing.T) {
	t.Parallel()
	off := false
	r := tccRig(t, api.Role{TCCDeny: &off})
	if err := r.store.UpdateTeam("w/squad", func(tm *api.Team) error { tm.Roles[0].SandboxProfile = r.profile; return nil }); err != nil {
		t.Fatal(err)
	}
	plan := r.mgr.planLaunch(r.session())
	if plan.sandbox == nil {
		t.Fatalf("plan = %+v", plan)
	}
	got, _ := os.ReadFile(plan.sandbox.copyPath)
	src, _ := os.ReadFile(r.profile)
	if string(got) != string(src) {
		t.Errorf("the opted-out profile changed:\n%s", got)
	}
}

// The deny block is not containment: an adapter that relaxes when a profile is in
// force must not read it as one. A headless auto goose role with only the default
// deny is still refused as auto-unsandboxed.
func TestTheDefaultDenyIsNotReadAsContainment(t *testing.T) {
	t.Parallel()
	r := tccRig(t, api.Role{})
	role := goosePlanRole("")
	if err := r.store.UpdateTeam("w/squad", func(tm *api.Team) error { tm.Roles = []api.Role{role}; return nil }); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "goose")
	writeFile(t, bin, "#!/bin/sh\nexit 0\n", 0o755)
	r.mgr.LookPath = func(string) (string, error) { return bin, nil }
	sess := r.session()
	sess.HarnessHome = ""
	sess.Runtime = role.Runtime
	plan := r.mgr.planLaunch(sess)
	if !strings.Contains(plan.command, "'goose-refuse' 'auto-unsandboxed'") {
		t.Errorf("command %q: the default deny was read as containment", plan.command)
	}
}

// A mount table read that fails leaves the static rows, and the seat still starts.
func TestAFailedMountReadStillDeniesTheStaticFolders(t *testing.T) {
	t.Parallel()
	r := tccRig(t, api.Role{})
	r.mgr.NetMounts = func() []string { return nil }
	plan := r.mgr.planLaunch(r.session())
	if plan.sandbox == nil {
		t.Fatalf("plan = %+v", plan)
	}
	got, _ := os.ReadFile(plan.sandbox.copyPath)
	if !strings.Contains(string(got), `(subpath "/Volumes")`) {
		t.Errorf("static rows missing:\n%s", got)
	}
}

func TestIsNetworkFS(t *testing.T) {
	t.Parallel()
	for _, fs := range []string{"nfs", "smbfs", "afpfs", "webdav"} {
		if !isNetworkFS(fs) {
			t.Errorf("isNetworkFS(%q) = false", fs)
		}
	}
	for _, fs := range []string{"apfs", "hfs", "devfs", "autofs", "msdos", "", "NFS"} {
		if isNetworkFS(fs) {
			t.Errorf("isNetworkFS(%q) = true", fs)
		}
	}
}

// On darwin the real mount table reads without error and never names a local
// volume as a network mount (the root volume is apfs).
func TestNetworkMountsNeverNamesTheRootVolume(t *testing.T) {
	t.Parallel()
	got := networkMounts()
	t.Logf("network mounts read: %v", got)
	for _, m := range got {
		if m == "/" {
			t.Error("networkMounts() names / as a network mount")
		}
	}
}
