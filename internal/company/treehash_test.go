package company

import (
	"os"
	"path/filepath"
	"testing"
)

// The worked vector of docs/design/companies.md ("The tree hash (v1)"): a file,
// a directory, an empty file whose name holds a newline, a file, and a link.
const workedVectorHash = "d44bd0c103e09a4e353971211d892ffcd9c5aad767eeff24a258211f7e0f5dcf"

func buildWorkedVector(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "company.toml"), []byte("name = \"x\"\n"), 0o444))
	must(os.Mkdir(filepath.Join(dir, "roles"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "roles", "a\nb.md"), nil, 0o444))
	must(os.WriteFile(filepath.Join(dir, "roles", "x.md"), []byte("hi\n"), 0o444))
	must(os.Symlink("roles/x.md", filepath.Join(dir, "top")))
	return dir
}

func TestTreeHashMatchesTheWorkedVector(t *testing.T) {
	dir := buildWorkedVector(t)
	got, err := TreeHash(dir)
	if err != nil {
		t.Fatalf("TreeHash: %v", err)
	}
	if got != workedVectorHash {
		t.Fatalf("tree hash = %s, want %s", got, workedVectorHash)
	}
}

// A link is recorded as a link and never followed: pointing the same link at a
// different target changes the hash, and so does changing the target's bytes
// when the target is a file (a file is hashed by content, a link by its text).
func TestTreeHashRecordsALinkAndDoesNotFollowIt(t *testing.T) {
	dir := buildWorkedVector(t)
	if err := os.Remove(filepath.Join(dir, "top")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("roles/nowhere", filepath.Join(dir, "top")); err != nil {
		t.Fatal(err)
	}
	got, err := TreeHash(dir)
	if err != nil {
		t.Fatalf("a dangling link must hash, not fail: %v", err)
	}
	if got == workedVectorHash || got == "" {
		t.Fatalf("hash %q after retargeting the link, want a different non-empty hash", got)
	}
}

// Mode bits, owners and times are not hashed.
func TestTreeHashIgnoresModeBits(t *testing.T) {
	dir := buildWorkedVector(t)
	if err := os.Chmod(filepath.Join(dir, "roles", "x.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := TreeHash(dir)
	if err != nil || got != workedVectorHash {
		t.Fatalf("hash %q err %v after a chmod, want the worked vector", got, err)
	}
}

func TestTreeHashOfAMissingDirectoryIsAnError(t *testing.T) {
	if got, err := TreeHash(filepath.Join(t.TempDir(), "absent")); err == nil || got != "" {
		t.Fatalf("hash %q err %v, want an error and no hash", got, err)
	}
}
