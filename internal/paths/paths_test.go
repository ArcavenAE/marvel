package paths

import (
	"os"
	"path/filepath"
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
