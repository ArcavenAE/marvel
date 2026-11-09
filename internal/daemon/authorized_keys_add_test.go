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
	after, _ := os.ReadFile(layout.AuthorizedKeys())
	if string(after) != string(before) {
		t.Errorf("file changed by a refused add:\nbefore %q\nafter  %q", before, after)
	}
}

// A single key that is already authorized, wherever it sits in the existing
// file, is refused with its fingerprint and the file is left as it was.
func TestAddAuthorizedKeyDuplicateNamesTheFingerprint(t *testing.T) {
	layout := addKeyEnv(t)
	a, b := testPubKeyLine(t), testPubKeyLine(t)
	for _, k := range [][]byte{a, b} {
		if err := AddAuthorizedKey(k, "seed", ScopeAdmin); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	before, _ := os.ReadFile(layout.AuthorizedKeys())

	err := AddAuthorizedKey(b, "again", ScopeCredentialPush)
	if err == nil || !strings.Contains(err.Error(), keyFingerprint(t, b)) {
		t.Fatalf("err = %v, want one naming %s", err, keyFingerprint(t, b))
	}
	after, _ := os.ReadFile(layout.AuthorizedKeys())
	if string(after) != string(before) {
		t.Errorf("file changed by a refused add")
	}
}

func TestAddAuthorizedKeyRefusesWhatItCannotPlaceAtOneScope(t *testing.T) {
	key := strings.TrimSpace(string(testPubKeyLine(t)))
	cases := map[string]struct {
		input string
		want  string
	}{
		"trailing text":    {key + "\nnot a key\n", "unexpected text"},
		"text before key":  {"not a key\n" + key + "\n", "unexpected text"},
		"input option":     {`command="x" ` + key + "\n", `"command"`},
		"input scope":      {`marvel-scope="admin" ` + key + "\n", `"marvel-scope"`},
		"only comments":    {"# nothing here\n\n", "no key found"},
		"empty":            {"", "no key found"},
		"two keys on file": {key + "\n" + string(testPubKeyLine(t)), "2 keys"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			layout := addKeyEnv(t)
			err := AddAuthorizedKey([]byte(tc.input), "x", ScopeCredentialPush)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
			if _, serr := os.Stat(layout.AuthorizedKeys()); !os.IsNotExist(serr) {
				t.Errorf("a refused input left authorized_keys behind (%v)", serr)
			}
		})
	}
}

// The parser's own leniency (CRLF, no final newline, blank lines, comments)
// still gives one key at the requested scope, and the line written is rebuilt
// from the parsed key: the key's comment is kept, the input's comment lines
// and carriage returns are not.
func TestAddAuthorizedKeyAcceptsPlainVariants(t *testing.T) {
	key := strings.TrimSpace(string(testPubKeyLine(t)))
	for name, input := range map[string]string{
		"crlf":             "# c\r\n\r\n" + key + " alice@laptop\r\n",
		"no final newline": key + " alice@laptop",
		"blank lines":      "\n\n# c\n" + key + " alice@laptop\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			layout := addKeyEnv(t)
			if err := AddAuthorizedKey([]byte(input), "x", ScopeCredentialPush); err != nil {
				t.Fatalf("add: %v", err)
			}
			entries, err := loadAuthorizedKeys(layout)
			if err != nil || len(entries) != 1 || entries[0].scope != ScopeCredentialPush {
				t.Fatalf("entries = %+v, err %v; want one credential-push key", entries, err)
			}
			data, _ := os.ReadFile(layout.AuthorizedKeys())
			if strings.ContainsAny(string(data), "\r#") {
				t.Errorf("written file carries input bytes it should not: %q", data)
			}
			listed, err := ListAuthorizedKeys()
			if err != nil || len(listed) != 1 || listed[0].Comment != "alice@laptop" {
				t.Errorf("listed = %+v, err %v; want the key comment kept", listed, err)
			}
		})
	}
}

func TestAddAuthorizedKeyAppendsAndKeepsMode(t *testing.T) {
	layout := addKeyEnv(t)
	if err := layout.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	seed := strings.TrimSpace(string(testPubKeyLine(t))) + "\n"
	if err := os.WriteFile(layout.AuthorizedKeys(), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AddAuthorizedKey(testPubKeyLine(t), "x", ScopeCredentialPush); err != nil {
		t.Fatalf("add: %v", err)
	}
	entries, err := loadAuthorizedKeys(layout)
	if err != nil || len(entries) != 2 || entries[0].scope != ScopeAdmin || entries[1].scope != ScopeCredentialPush {
		t.Fatalf("entries = %+v, err %v; want the seed (admin) then the new key (credential-push)", entries, err)
	}
	st, err := os.Stat(layout.AuthorizedKeys())
	if err != nil || st.Mode().Perm() != paths.ModeAuthorized {
		t.Errorf("mode = %v (err %v), want %v", st.Mode().Perm(), err, paths.ModeAuthorized)
	}
}

// A byte order mark is not text the ssh parser reads past: a key line that
// starts with one does not parse as a key, so the input is refused rather
// than written. Pinned by running the parser.
func TestAddAuthorizedKeyBOM(t *testing.T) {
	layout := addKeyEnv(t)
	input := "\ufeff" + strings.TrimSpace(string(testPubKeyLine(t))) + "\n"
	err := AddAuthorizedKey([]byte(input), "x", ScopeCredentialPush)
	t.Logf("BOM result: err=%v", err)
	if err == nil {
		entries, _ := loadAuthorizedKeys(layout)
		t.Fatalf("BOM input accepted: %+v", entries)
	}
}

// A refused input creates nothing: not authorized_keys, and not the marvel home
// either (marvel#1330).
func TestAddAuthorizedKeyRefusalLeavesNoHome(t *testing.T) {
	layout := addKeyEnv(t)
	home := filepath.Dir(layout.AuthorizedKeys())
	two := append(testPubKeyLine(t), testPubKeyLine(t)...)

	if err := AddAuthorizedKey(two, "two", ScopeCredentialPush); err == nil {
		t.Fatal("a two-key file was accepted")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("a refused file created the marvel home %s (stat err %v)", home, err)
	}
}

// An authorized_keys that cannot be read must fail the enroll, never skip the
// duplicate check and append. The file is write-only here, so the read fails
// while an append would succeed (marvel#1330).
func TestAddAuthorizedKeyUnreadableFileFailsAndWritesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a write-only file")
	}
	layout := addKeyEnv(t)
	if err := layout.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	path := layout.AuthorizedKeys()
	key := testPubKeyLine(t)
	seed := append([]byte(`marvel-scope="admin" `), key...)
	if err := os.WriteFile(path, seed, paths.ModeAuthorized); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o200); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, paths.ModeAuthorized) })

	if err := AddAuthorizedKey(key, "again", ScopeCredentialPush); err == nil {
		t.Fatal("an unreadable authorized_keys let the enroll proceed")
	}
	if err := os.Chmod(path, paths.ModeAuthorized); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(seed) {
		t.Fatalf("authorized_keys changed:\nbefore %q\nafter  %q", seed, got)
	}
}
