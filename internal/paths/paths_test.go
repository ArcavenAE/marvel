package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayout(t *testing.T) {
	l := WithHome("/root/.marvel")

	tests := []struct {
		got, want string
	}{
		{l.Config(), "/root/.marvel/config.yaml"},
		{l.AuthorizedKeys(), "/root/.marvel/authorized_keys"},
		{l.HostKey(), "/root/.marvel/ssh_host_ed25519_key"},
		{l.HostKeyPub(), "/root/.marvel/ssh_host_ed25519_key.pub"},
		{l.KnownHosts(), "/root/.marvel/known_hosts"},
		{l.KeysDir(), "/root/.marvel/keys"},
		{l.ClientKey("foo"), "/root/.marvel/keys/foo"},
		{l.ClientKeyPub("foo"), "/root/.marvel/keys/foo.pub"},
		{l.DefaultClientKey(), "/root/.marvel/keys/" + DefaultClientKeyName},
		{l.LogDir(), "/root/.marvel/log"},
		{l.RunDir(), "/root/.marvel/run"},
		{l.DaemonLog(), "/root/.marvel/log/daemon.log"},
		{l.DaemonPid(), "/root/.marvel/run/daemon.pid"},
	}

	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("path mismatch: got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestEnsureHome(t *testing.T) {
	dir := t.TempDir()
	l := WithHome(filepath.Join(dir, ".marvel"))

	if err := l.EnsureHome(); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}

	info, err := os.Stat(l.Home)
	if err != nil {
		t.Fatalf("stat home: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("home is not a directory")
	}
	if info.Mode().Perm() != ModeDir {
		t.Errorf("home mode: got %o, want %o", info.Mode().Perm(), ModeDir)
	}

	// Second call should be a no-op.
	if err := l.EnsureHome(); err != nil {
		t.Fatalf("EnsureHome (repeat): %v", err)
	}
}

func TestEnsureKeysDir(t *testing.T) {
	dir := t.TempDir()
	l := WithHome(filepath.Join(dir, ".marvel"))

	if err := l.EnsureKeysDir(); err != nil {
		t.Fatalf("EnsureKeysDir: %v", err)
	}

	info, err := os.Stat(l.KeysDir())
	if err != nil {
		t.Fatalf("stat keys dir: %v", err)
	}
	if info.Mode().Perm() != ModeDir {
		t.Errorf("keys dir mode: got %o, want %o", info.Mode().Perm(), ModeDir)
	}
}

func TestEnsureLogAndRunDirs(t *testing.T) {
	dir := t.TempDir()
	l := WithHome(filepath.Join(dir, ".marvel"))

	if err := l.EnsureLogDir(); err != nil {
		t.Fatalf("EnsureLogDir: %v", err)
	}
	if err := l.EnsureRunDir(); err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}

	for _, p := range []string{l.LogDir(), l.RunDir()} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if info.Mode().Perm() != ModeDir {
			t.Errorf("%s mode: got %o, want %o", p, info.Mode().Perm(), ModeDir)
		}
	}
}

// The socket is the isolation unit for concurrent daemons, so it has to
// track the layout and nothing else. The predecessor resolved through
// XDG_RUNTIME_DIR or /tmp independent of HOME, which is what let two
// HOME-separated daemons collide. See aae-orc-t6da.
func TestRuntimeSocketFollowsTheLayout(t *testing.T) {
	// Set in the environment the old free function consulted, to prove
	// the layout-derived path no longer answers to it.
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	a := WithHome("/home/alice/.marvel")
	b := WithHome("/home/bob/.marvel")

	if got, want := a.RuntimeSocket(), "/home/alice/.marvel/run/marvel.sock"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if a.RuntimeSocket() == b.RuntimeSocket() {
		t.Errorf("two layouts share a socket path (%q); they must not", a.RuntimeSocket())
	}
	if filepath.Dir(a.RuntimeSocket()) != a.RunDir() {
		t.Errorf("socket is not in RunDir: %q vs %q", a.RuntimeSocket(), a.RunDir())
	}
}

// The tmux server is the second isolation unit, and the destructive one:
// two daemons sharing it means either can reach the other's whole running
// fleet. See docs/design/daemon-isolation.md decision 4, aae-orc-by6j.
func TestTmuxSocketNameFollowsTheLayout(t *testing.T) {
	a := WithHome("/home/alice/.marvel")
	b := WithHome("/home/bob/.marvel")

	if a.TmuxSocketName() == b.TmuxSocketName() {
		t.Errorf("two layouts share a tmux server (%q); they must not", a.TmuxSocketName())
	}

	// Stability across restarts under the same HOME. Without this,
	// adopt-on-restart breaks and every restart orphans its own fleet.
	if got, want := WithHome("/home/alice/.marvel").TmuxSocketName(), a.TmuxSocketName(); got != want {
		t.Errorf("name is not stable for one home: %q then %q", want, got)
	}

	// Shape: marvel- plus exactly 8 lowercase hex digits.
	name := a.TmuxSocketName()
	rest, ok := strings.CutPrefix(name, "marvel-")
	if !ok {
		t.Fatalf("name %q does not start with marvel-", name)
	}
	if len(rest) != 8 {
		t.Errorf("name %q has a %d-digit suffix, want 8", name, len(rest))
	}
	for _, r := range rest {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Errorf("name %q has non-hex digit %q", name, r)
			break
		}
	}
}

// tmux refused a 109-byte socket name with "File name too long" and
// marvel emitted nothing at all: sessions stayed pending forever. The
// name must not grow with the depth of HOME, so measure it against a
// home far deeper than the failing case.
func TestTmuxSocketNameStaysShortForADeepHome(t *testing.T) {
	deep := WithHome("/" + strings.Repeat("verylongdirectorysegment/", 20) + ".marvel")
	if len(deep.Home) < 109 {
		t.Fatalf("test home is only %d bytes; it is meant to exceed the 109-byte case", len(deep.Home))
	}
	if got := len(deep.TmuxSocketName()); got != 15 {
		t.Errorf("name for a %d-byte home is %d bytes (%q), want 15",
			len(deep.Home), got, deep.TmuxSocketName())
	}
}

func TestCheckSocketPath(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"layout default is comfortably under", "/Users/someone/.marvel/run/marvel.sock", false},
		{"exactly at the limit", strings.Repeat("a", MaxUnixSocketPath), false},
		{"one over the limit", strings.Repeat("a", MaxUnixSocketPath+1), true},
		// A 136-byte path under a session scratchpad was hit by accident
		// while probing this; it is the case the assertion exists for.
		{"deep scratchpad path", "/private/tmp/claude-501/" + strings.Repeat("b", 120) + "/marvel.sock", true},
		// TCP addresses have no sun_path ceiling.
		{"tcp is not checked", "0.0.0.0:9090", false},
		{"long tcp is not checked", strings.Repeat("h", 200) + ":9090", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckSocketPath(tc.path)
			if tc.wantErr && err == nil {
				t.Fatalf("CheckSocketPath(%d bytes) = nil, want error", len(tc.path))
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("CheckSocketPath(%d bytes) = %v, want nil", len(tc.path), err)
			}
			// The error has to name the number, or it sends the reader
			// looking in the wrong place.
			if err != nil && !strings.Contains(err.Error(), fmt.Sprint(len(tc.path))) {
				t.Errorf("error does not name the length: %v", err)
			}
		})
	}
}

// IsTCPAddr is the one definition of the host:port rule, shared by the
// daemon's listen and dial routing, the socket length check, and the
// client's self-report comparison. These cases are the vocabulary all
// three see.
func TestIsTCPAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"/Users/someone/.marvel/run/marvel.sock", false},
		{"/tmp/marvel.sock", false},
		{"marvel.sock", false},
		{"0.0.0.0:9090", true},
		{":9090", true},
		{"tcp://host:9090", true},
		{"mrvl://host", true},
		{"ssh://op@host/run/marvel.sock", true},
	}

	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			if got := IsTCPAddr(tc.addr); got != tc.want {
				t.Errorf("IsTCPAddr(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

func TestCheckMode(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		kind    Kind
		mode    os.FileMode
		wantErr bool
	}{
		{"dir_ok", KindDir, 0o700, false},
		{"dir_group_readable", KindDir, 0o750, true},
		{"dir_world_readable", KindDir, 0o755, true},
		{"private_ok", KindPrivate, 0o600, false},
		{"private_too_open", KindPrivate, 0o644, true},
		{"private_group_readable", KindPrivate, 0o640, true},
		{"public_ok", KindPublic, 0o644, false},
		{"public_too_open", KindPublic, 0o666, true},
		{"config_ok", KindConfig, 0o600, false},
		{"config_too_open", KindConfig, 0o644, true},
		{"known_hosts_ok", KindKnownHosts, 0o644, false},
		{"known_hosts_writable", KindKnownHosts, 0o666, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name)
			if tc.kind == KindDir {
				if err := os.Mkdir(p, tc.mode); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			} else {
				if err := os.WriteFile(p, []byte("x"), tc.mode); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			// umask can interfere — force the mode we want.
			if err := os.Chmod(p, tc.mode); err != nil {
				t.Fatalf("chmod: %v", err)
			}

			issue, err := CheckMode(p, tc.kind)
			if err != nil {
				t.Fatalf("CheckMode: %v", err)
			}
			if tc.wantErr && issue == nil {
				t.Errorf("expected issue, got nil")
			}
			if !tc.wantErr && issue != nil {
				t.Errorf("unexpected issue: %s", issue.Error())
			}
		})
	}
}

func TestAuditAndRepair(t *testing.T) {
	dir := t.TempDir()
	l := WithHome(filepath.Join(dir, ".marvel"))

	if err := l.EnsureKeysDir(); err != nil {
		t.Fatalf("EnsureKeysDir: %v", err)
	}

	// Create a host key with wrong (too open) permissions.
	if err := os.WriteFile(l.HostKey(), []byte("fake-key"), 0o644); err != nil {
		t.Fatalf("write host key: %v", err)
	}
	// And a pubkey with too-open write perms.
	if err := os.WriteFile(l.HostKeyPub(), []byte("fake-pub"), 0o666); err != nil {
		t.Fatalf("write pub: %v", err)
	}
	// Force modes past umask.
	_ = os.Chmod(l.HostKey(), 0o644)
	_ = os.Chmod(l.HostKeyPub(), 0o666)

	issues, err := l.Audit()
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if len(issues) < 2 {
		t.Fatalf("expected >=2 issues, got %d: %v", len(issues), issues)
	}

	remaining := l.Repair(issues)
	if len(remaining) != 0 {
		t.Errorf("repair left issues: %v", remaining)
	}

	// After repair, audit should be clean.
	issues2, err := l.Audit()
	if err != nil {
		t.Fatalf("Audit after repair: %v", err)
	}
	if len(issues2) != 0 {
		t.Errorf("audit after repair not clean: %v", issues2)
	}
}

func TestVerifyPrivateKeyMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "key")

	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = os.Chmod(p, 0o600)
	if err := VerifyPrivateKeyMode(p); err != nil {
		t.Errorf("unexpected error for 0600 key: %v", err)
	}

	_ = os.Chmod(p, 0o644)
	if err := VerifyPrivateKeyMode(p); err == nil {
		t.Error("expected error for 0644 key")
	}

	if err := VerifyPrivateKeyMode(filepath.Join(dir, "nope")); err == nil {
		t.Error("expected error for missing key")
	}
}
