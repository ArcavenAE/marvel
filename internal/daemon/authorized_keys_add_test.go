package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/paths"
)

// addKeyEnv points the daemon's home at a temp dir and returns its layout.
// Not parallel: it overrides HOME so paths.Default resolves into the temp dir.
func addKeyEnv(t *testing.T) paths.Layout {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv(paths.HomeEnv, "")
	return paths.WithHome(filepath.Join(dir, ".marvel"))
}

func keyFingerprint(t *testing.T, line []byte) string {
	t.Helper()
	k, _, _, _, err := ssh.ParseAuthorizedKey(line)
	if err != nil {
		t.Fatal(err)
	}
	return ssh.FingerprintSHA256(k)
}

// A file with two keys enrolls neither at a wider scope than asked: the
// command refuses it and writes nothing (marvel#1330).
func TestAddAuthorizedKeyRefusesTwoKeyFile(t *testing.T) {
	layout := addKeyEnv(t)
	two := append(testPubKeyLine(t), testPubKeyLine(t)...)

	err := AddAuthorizedKey(two, "two", ScopeCredentialPush)
	if err == nil {
		entries, _ := loadAuthorizedKeys(layout)
		t.Fatalf("a two-key file was accepted; loaded scopes: %+v", entries)
	}
	if _, serr := os.Stat(layout.AuthorizedKeys()); !os.IsNotExist(serr) {
		t.Fatalf("a refused file left authorized_keys behind (stat err %v)", serr)
	}
}

// A comment above a single key must not take the scope option: the key line
// carries it (marvel#1330).
func TestAddAuthorizedKeyLeadingCommentSingleKey(t *testing.T) {
	layout := addKeyEnv(t)
	file := append([]byte("# for the CI runner\n"), testPubKeyLine(t)...)

	if err := AddAuthorizedKey(file, "commented", ScopeCredentialPush); err != nil {
		t.Fatalf("add: %v", err)
	}
	entries, err := loadAuthorizedKeys(layout)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].scope != ScopeCredentialPush {
		t.Fatalf("entries = %+v, want one credential-push key", entries)
	}
	data, err := os.ReadFile(layout.AuthorizedKeys())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.HasPrefix(line, `marvel-scope="credential-push" `) {
			t.Errorf("written line does not start with the scope option: %q", line)
		}
	}
}

// A two-key buffer holding a key that is already authorized is refused and
// the file is left byte for byte as it was (marvel#1330).
func TestAddAuthorizedKeyDuplicateSecondKey(t *testing.T) {
	layout := addKeyEnv(t)
	b := testPubKeyLine(t)
	a := testPubKeyLine(t)
	if err := AddAuthorizedKey(b, "b", ScopeAdmin); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, err := os.ReadFile(layout.AuthorizedKeys())
	if err != nil {
		t.Fatal(err)
	}

	err = AddAuthorizedKey(append(a, b...), "ab", ScopeCredentialPush)
	if err == nil {
		t.Fatal("a buffer holding an already authorized key was accepted")
	}
	if want := keyFingerprint(t, b); !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name the duplicate %s", err, want)
	}
	after, _ := os.ReadFile(layout.AuthorizedKeys())
	if string(after) != string(before) {
		t.Errorf("file changed by a refused add:\nbefore %q\nafter  %q", before, after)
	}
}
