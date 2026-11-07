package register

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

var pullAt = time.Date(2026, 11, 6, 18, 0, 0, 0, time.UTC)

func pullRows() []Row {
	return []Row{
		Build(Input{Seat: "ws/a-g1-0", Duties: []string{"first-turn", "ack"}}),
		fixtureRow(),
	}
}

func objectKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not an object: %v\n%s", err, raw)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The pull file is created 0600, replaces the old file by rename, carries the
// same keys as the RPC's rows and the one pass-time key, and reads as not run
// once it is older than the limit a reader passes.
func TestRegisterPullFileWithoutCLI(t *testing.T) {
	dir := t.TempDir()
	rows := pullRows()
	if err := WriteFile(dir, PullFileName, rows, pullAt); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, PullFileName)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != PullFileName {
		t.Errorf("directory holds %v, want only %s and no temp file", entries, PullFileName)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := objectKeys(t, raw), []string{"rows", "written_at"}; !reflect.DeepEqual(got, want) {
		t.Errorf("top-level keys = %v, want %v", got, want)
	}
	var file struct {
		Rows []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	rpc, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var rpcRows []json.RawMessage
	if err := json.Unmarshal(rpc, &rpcRows); err != nil {
		t.Fatal(err)
	}
	if len(file.Rows) != len(rpcRows) {
		t.Fatalf("file has %d rows, the RPC shape has %d", len(file.Rows), len(rpcRows))
	}
	for i := range rpcRows {
		if !reflect.DeepEqual(objectKeys(t, file.Rows[i]), objectKeys(t, rpcRows[i])) {
			t.Errorf("row %d keys differ: file %v, RPC %v", i, objectKeys(t, file.Rows[i]), objectKeys(t, rpcRows[i]))
		}
		if !bytes.Equal(file.Rows[i], rpcRows[i]) {
			t.Errorf("row %d bytes differ from the RPC's row", i)
		}
	}

	fresh, err := ReadFile(path, time.Minute, pullAt.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.NotRun != "" || len(fresh.Rows) != len(rows) || !fresh.WrittenAt.Equal(pullAt) {
		t.Errorf("fresh read = %+v", fresh)
	}
	stale, err := ReadFile(path, time.Minute, pullAt.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if stale.NotRun != "not-run since 2026-11-06T18:00:00Z" || len(stale.Rows) != 0 {
		t.Errorf("stale read = %+v, want not-run since the pass time and no rows", stale)
	}
}

// A reader that looks at the destination between the temp file's creation and
// the rename sees the previous complete file, never an empty or partial one.
func TestRegisterPullFileReaderMidWriteSeesTheWholeOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PullFileName)

	var seen []byte
	var seenErr error
	hook := func() { seen, seenErr = os.ReadFile(path) }

	// First write: nothing exists yet, so a mid-write reader finds no file.
	if err := writeFile(dir, PullFileName, pullRows(), pullAt, hook); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(seenErr, os.ErrNotExist) {
		t.Errorf("mid-write read of a first write = %q, %v; want no file", seen, seenErr)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Second write: the reader must see exactly the first file.
	seen, seenErr = nil, nil
	if err := writeFile(dir, PullFileName, pullRows()[:1], pullAt.Add(time.Minute), hook); err != nil {
		t.Fatal(err)
	}
	if seenErr != nil || !bytes.Equal(seen, old) {
		t.Errorf("mid-write read = %q, %v; want the previous file unchanged", seen, seenErr)
	}
	var p Pull
	if err := json.Unmarshal(seen, &p); err != nil {
		t.Errorf("mid-write read is not a whole file: %v", err)
	}
}

func TestRegisterPullFileRefusals(t *testing.T) {
	dir := t.TempDir()
	notDir := filepath.Join(dir, "plain")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"a missing directory":     func() error { return WriteFile(filepath.Join(dir, "nope"), PullFileName, nil, pullAt) },
		"a file as the directory": func() error { return WriteFile(notDir, PullFileName, nil, pullAt) },
		"a name with a slash":     func() error { return WriteFile(dir, "a/b", nil, pullAt) },
		"an empty name":           func() error { return WriteFile(dir, "", nil, pullAt) },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("refusals left files behind: %v", entries)
	}
}

// A directory another user owns is refused for its owner, before any temp file
// is tried. The filesystem root stands in for it: root owns it, so a test run
// as root cannot use it.
func TestRegisterPullFileRefusesADirectoryTheDaemonUserDoesNotOwn(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("the root directory is owned by the user running the test")
	}
	err := WriteFile("/", PullFileName, nil, pullAt)
	if err == nil || !strings.Contains(err.Error(), "not owned by the daemon's user") {
		t.Fatalf("WriteFile(/) = %v, want the ownership refusal", err)
	}
}

// A rename that fails leaves no temp file and does not touch the target.
func TestRegisterPullFileFailedRenameLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, PullFileName)
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(dir, PullFileName, pullRows(), pullAt); err == nil {
		t.Fatal("renaming a file over a non-empty directory succeeded")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != PullFileName {
		t.Errorf("directory holds %v, want only the untouched %s", entries, PullFileName)
	}
}

func TestReadFileRefusals(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, PullFileName)
	if err := WriteFile(dir, PullFileName, pullRows(), pullAt); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(good, 0, pullAt); !errors.Is(err, ErrNoMaxAge) {
		t.Errorf("zero max age: %v, want ErrNoMaxAge", err)
	}
	if _, err := ReadFile(good, -time.Second, pullAt); !errors.Is(err, ErrNoMaxAge) {
		t.Errorf("negative max age: %v, want ErrNoMaxAge", err)
	}
	// A missing file is an error, never "not run since".
	r, err := ReadFile(filepath.Join(dir, "absent.json"), time.Minute, pullAt)
	if err == nil || r.NotRun != "" {
		t.Errorf("missing file: %+v, %v; want an error and no stale text", r, err)
	}
	bad := filepath.Join(dir, "bad.json")
	for name, body := range map[string]string{"corrupt": "{", "no pass time": `{"rows":[]}`} {
		if err := os.WriteFile(bad, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if r, err := ReadFile(bad, time.Minute, pullAt); err == nil || r.NotRun != "" {
			t.Errorf("%s: %+v, %v; want an error", name, r, err)
		}
	}
	// A pass exactly at the limit is still current.
	r, err = ReadFile(good, time.Minute, pullAt.Add(time.Minute))
	if err != nil || r.NotRun != "" {
		t.Errorf("a pass at exactly the limit: %+v, %v", r, err)
	}
}
