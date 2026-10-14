package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

// The layout root has one selector, MARVEL_HOME, which the --marvel-home flag
// sets. Unset, the layout is $HOME/.marvel exactly as before.

func TestDefaultLayoutRootPrecedence(t *testing.T) {
	user := t.TempDir()
	root := filepath.Join(t.TempDir(), "mh")

	cases := []struct {
		name    string
		env     *string // nil leaves the variable unset
		want    string
		wantErr string
	}{
		{name: "unset is the home directory's .marvel", want: filepath.Join(user, ".marvel")},
		{name: "empty is unset", env: ptr(""), want: filepath.Join(user, ".marvel")},
		{name: "absolute is the root", env: ptr(root), want: root},
		{name: "cleaned", env: ptr(root + "/a/../"), want: root},
		{name: "relative is refused", env: ptr("rel/dir"), wantErr: `"rel/dir"`},
		{name: "dot is refused", env: ptr("."), wantErr: `"."`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", user)
			if tc.env == nil {
				t.Setenv(HomeEnv, "")
			} else {
				t.Setenv(HomeEnv, *tc.env)
			}
			l, err := Default()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), HomeEnv) {
					t.Fatalf("Default() error = %v, want one naming %s and %s", err, HomeEnv, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Default(): %v", err)
			}
			if l.Home != tc.want {
				t.Errorf("Home = %q, want %q", l.Home, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestParseHomeRefusals(t *testing.T) {
	for _, v := range []string{"", "rel", "./x", "~/x", "x/../y"} {
		if _, err := ParseHome(v, "--marvel-home"); err == nil {
			t.Errorf("ParseHome(%q) accepted a value that is not an absolute path", v)
		} else if !strings.Contains(err.Error(), "--marvel-home") || !strings.Contains(err.Error(), `"`+v+`"`) {
			t.Errorf("ParseHome(%q) error %q does not name the source and the value", v, err)
		}
	}
	if l, err := ParseHome("/a/b/../c", "--marvel-home"); err != nil || l.Home != "/a/c" {
		t.Errorf("ParseHome(/a/b/../c) = %q, %v; want /a/c", l.Home, err)
	}
}

func TestHomeRefusesARootWhoseSocketIsTooLong(t *testing.T) {
	long := "/" + strings.Repeat("d", MaxUnixSocketPath)
	if _, err := ParseHome(long, "--marvel-home"); err == nil || !strings.Contains(err.Error(), "unix socket") {
		t.Fatalf("a root %d bytes long gave %v, want the socket-length refusal", len(long), err)
	}
	t.Setenv(HomeEnv, long)
	if _, err := Default(); err == nil || !strings.Contains(err.Error(), "unix socket") {
		t.Fatalf("Default() with a long %s gave %v, want the socket-length refusal", HomeEnv, err)
	}
	// A root that just fits is accepted: the refusal is the limit, not a margin.
	fit := "/" + strings.Repeat("d", MaxUnixSocketPath-len("/run/marvel.sock")-1)
	if l, err := ParseHome(fit, "--marvel-home"); err != nil || len(l.RuntimeSocket()) > MaxUnixSocketPath {
		t.Errorf("a root whose socket is %d bytes was refused or too long: %v", len(l.RuntimeSocket()), err)
	}
}

func TestTwoRootsAreTwoTags(t *testing.T) {
	a, err := ParseHome("/tmp/mh-a", "t")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseHome("/tmp/mh-b", "t")
	if err != nil {
		t.Fatal(err)
	}
	if a.Tag() == b.Tag() || a.TmuxSocketName() == b.TmuxSocketName() {
		t.Errorf("two roots share a tag or a tmux name: %s %s", a.Tag(), b.Tag())
	}
	if a.RuntimeSocket() == b.RuntimeSocket() || a.DaemonBolt() == b.DaemonBolt() || a.DaemonPid() == b.DaemonPid() {
		t.Errorf("two roots share a socket, bolt or pid file")
	}
}

func TestNonDefaultHome(t *testing.T) {
	user := t.TempDir()
	root := filepath.Join(t.TempDir(), "mh")
	t.Setenv("HOME", user)

	t.Setenv(HomeEnv, "")
	if got := NonDefaultHome(); got != "" {
		t.Errorf("unset: NonDefaultHome() = %q, want empty", got)
	}
	t.Setenv(HomeEnv, filepath.Join(user, ".marvel"))
	if got := NonDefaultHome(); got != "" {
		t.Errorf("set to the default: NonDefaultHome() = %q, want empty", got)
	}
	t.Setenv(HomeEnv, root)
	if got := NonDefaultHome(); got != root {
		t.Errorf("set to another root: NonDefaultHome() = %q, want %q", got, root)
	}
	t.Setenv(HomeEnv, "rel")
	if got := NonDefaultHome(); got != "" {
		t.Errorf("an invalid value: NonDefaultHome() = %q, want empty (Default reports the error)", got)
	}
}
