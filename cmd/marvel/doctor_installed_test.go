package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sha1 = "0123456789abcdef0123456789abcdef01234567"
	sha2 = "89abcdef0123456789abcdef0123456789abcdef"
)

// installFixture lays out scratch install records: a role-library work tree, a
// director home whose cast-launch and director-mcp links point into version
// directories, and an update log. Nothing here is the real install.
type installFixture struct {
	src installSource
}

func newInstallFixture(t *testing.T) installFixture {
	t.Helper()
	root := t.TempDir()
	f := installFixture{src: installSource{
		RoleLibraryDir: filepath.Join(root, "rl"),
		RoleLibraryJob: "rl-job",
		DirectorHome:   filepath.Join(root, "director"),
		UpdateLog:      filepath.Join(root, "state", "update.log"),
	}}
	return f
}

func (f installFixture) roleLibrary(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.src.RoleLibraryDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (f installFixture) link(t *testing.T, name, sha string) {
	t.Helper()
	ver := filepath.Join(f.src.DirectorHome, "versions", name, sha)
	if err := os.MkdirAll(ver, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(f.src.DirectorHome, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ver, name), filepath.Join(bin, name)); err != nil {
		t.Fatal(err)
	}
}

func (f installFixture) log(t *testing.T, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.src.UpdateLog), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.src.UpdateLog, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func installedHost(t *testing.T, f installFixture, head string, headErr error) doctorInput {
	t.Helper()
	in := fakeHost(t)
	base := in.Run
	in.Run = func(name string, args ...string) (string, error) {
		if name == "git" {
			if headErr != nil {
				return "", headErr
			}
			return head + "\n", nil
		}
		return base(name, args...)
	}
	in.Installs = f.src
	return in
}

// Given all three installs and a job log, doctor prints one line per install
// with the version it found and the job's last word on it.
func TestDoctorInstalledReportsEachRecord(t *testing.T) {
	f := newInstallFixture(t)
	f.roleLibrary(t)
	f.link(t, "cast-launch", sha1)
	f.link(t, "director-mcp", sha2)
	f.log(t, strings.Join([]string{
		"2026-11-08T10:00:00Z rl-job installed aaa -> " + sha1,
		"2026-11-08T10:00:01Z director-cast-launch unchanged at " + sha1,
		"2026-11-08T10:00:02Z director-mcp installed old -> " + sha2,
		"2026-11-08T11:00:00Z rl-job unchanged at " + sha1,
		"",
	}, "\n"))
	out, failed := doctorRun(installedHost(t, f, sha1, nil))
	if failed != 0 {
		t.Fatalf("failed = %d, want 0\n%s", failed, out)
	}
	w := lineWith(out, "role-library")
	if !strings.Contains(w, sha1[:12]) || !strings.Contains(w, "unchanged") || !strings.Contains(w, "2026-11-08T11:00:00Z") {
		t.Errorf("role-library line = %q, want the head and the last job line", w)
	}
	if strings.Contains(w, "installed aaa") {
		t.Errorf("role-library line quotes an older job line: %q", w)
	}
	c := lineWith(out, "cast-launch")
	if !strings.Contains(c, sha1[:12]) || !strings.Contains(c, "unchanged") {
		t.Errorf("cast-launch line = %q", c)
	}
	m := lineWith(out, "director-mcp")
	if !strings.Contains(m, sha2[:12]) || !strings.Contains(m, "installed") {
		t.Errorf("director-mcp line = %q", m)
	}
}

// Given no install at all, each line reads "not installed" and nothing fails or
// is created.
func TestDoctorInstalledMissingRecordsAreNotInstalled(t *testing.T) {
	f := newInstallFixture(t)
	out, failed := doctorRun(installedHost(t, f, "", nil))
	if failed != 0 {
		t.Fatalf("failed = %d, want 0\n%s", failed, out)
	}
	for _, name := range []string{"role-library", "cast-launch", "director-mcp"} {
		if l := lineWith(out, name); !strings.Contains(l, "not installed") {
			t.Errorf("%s line = %q, want not installed", name, l)
		}
	}
	for _, p := range []string{f.src.RoleLibraryDir, f.src.DirectorHome, filepath.Dir(f.src.UpdateLog)} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("doctor created %s", p)
		}
	}
}

// Given records it cannot read, each line reads "unreadable" and the exit
// count stays zero: a record problem is never a failure.
func TestDoctorInstalledUnreadableNeverFails(t *testing.T) {
	f := newInstallFixture(t)
	f.roleLibrary(t)
	f.link(t, "cast-launch", "not-a-sha")
	// the log path is a directory, so it cannot be read as a file
	if err := os.MkdirAll(f.src.UpdateLog, 0o755); err != nil {
		t.Fatal(err)
	}
	out, failed := doctorRun(installedHost(t, f, "", errors.New("git: not a repository")))
	if failed != 0 {
		t.Fatalf("failed = %d, want 0\n%s", failed, out)
	}
	if l := lineWith(out, "role-library"); !strings.Contains(l, "unreadable") {
		t.Errorf("role-library line = %q, want unreadable", l)
	}
	if l := lineWith(out, "cast-launch"); !strings.Contains(l, "unreadable") {
		t.Errorf("cast-launch line = %q, want unreadable", l)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("a record problem printed FAIL:\n%s", out)
	}
	if doctorExit(failed) != nil {
		t.Errorf("exit is nonzero for an unreadable record")
	}
}

// Given a log with no line for a component, the line says the job has not
// recorded it; given a stale line, doctor still prints it and fails nothing.
func TestDoctorInstalledStaleOrAbsentLogLineIsReportedNotJudged(t *testing.T) {
	f := newInstallFixture(t)
	f.roleLibrary(t)
	f.link(t, "director-mcp", sha2)
	f.log(t, "2020-01-01T00:00:00Z director-mcp refused main not verified\n")
	out, failed := doctorRun(installedHost(t, f, sha1, nil))
	if failed != 0 {
		t.Fatalf("failed = %d\n%s", failed, out)
	}
	if l := lineWith(out, "role-library"); !strings.Contains(l, "no job line") {
		t.Errorf("role-library line = %q, want no job line", l)
	}
	if l := lineWith(out, "director-mcp"); !strings.Contains(l, "refused") || !strings.Contains(l, "2020-01-01") {
		t.Errorf("director-mcp line = %q, want the refused line with its date", l)
	}
}

// Given a log line carrying control bytes and a long detail, the report is cut
// to a short printable run.
func TestDoctorInstalledSanitizesLogText(t *testing.T) {
	f := newInstallFixture(t)
	f.log(t, "2026-11-08T10:00:00Z rl-job refused "+strings.Repeat("x", 500)+"\x1b[31mred\n")
	out, _ := doctorRun(installedHost(t, f, "", nil))
	l := lineWith(out, "role-library")
	if strings.ContainsRune(l, 0x1b) {
		t.Errorf("line carries an escape byte: %q", l)
	}
	if len(l) > 200 {
		t.Errorf("line is %d bytes, want a short run", len(l))
	}
}

// Given the checks run, they only read: the records' metadata digest is
// unchanged, and the only program run for the role library is git rev-parse.
func TestDoctorInstalledIsReadOnly(t *testing.T) {
	f := newInstallFixture(t)
	f.roleLibrary(t)
	f.link(t, "cast-launch", sha1)
	f.log(t, "2026-11-08T10:00:00Z rl-job unchanged at "+sha1+"\n")
	root := filepath.Dir(f.src.RoleLibraryDir)
	before := homeDigest(t, root)
	in := installedHost(t, f, sha1, nil)
	var ran []string
	base := in.Run
	in.Run = func(name string, args ...string) (string, error) {
		if name == "git" {
			ran = append(ran, strings.Join(args, " "))
		}
		return base(name, args...)
	}
	_, _ = doctorRun(in)
	if after := homeDigest(t, root); after != before {
		t.Errorf("the install records changed")
	}
	if len(ran) != 1 || !strings.HasSuffix(ran[0], "rev-parse HEAD") {
		t.Errorf("git calls = %v, want one rev-parse HEAD", ran)
	}
}

// Given an empty Installs value (a caller that names no records), the checks
// print SKIP and read nothing.
func TestDoctorInstalledUnsetIsSkipped(t *testing.T) {
	in := fakeHost(t)
	out, failed := doctorRun(in)
	if failed != 0 {
		t.Fatalf("failed = %d\n%s", failed, out)
	}
	if l := lineWith(out, "installs"); !strings.Contains(l, "SKIP") {
		t.Errorf("installs line = %q, want SKIP", l)
	}
}

// Given a host that names no role-library directory, the line reads "not
// configured", which is not "not installed": a missing setting is never read as
// a missing install. The setting is named so the reader can fix it.
func TestDoctorInstalledUnsetDirReadsNotConfigured(t *testing.T) {
	f := newInstallFixture(t)
	f.src.RoleLibraryDir = ""
	out, failed := doctorRun(installedHost(t, f, "", nil))
	if failed != 0 {
		t.Fatalf("failed = %d\n%s", failed, out)
	}
	l := lineWith(out, "role-library")
	if !strings.Contains(l, "not configured") || strings.Contains(l, "not installed") {
		t.Errorf("role-library line = %q, want not configured", l)
	}
	if !strings.Contains(l, roleLibraryDirEnv) {
		t.Errorf("role-library line = %q, want it to name %s", l, roleLibraryDirEnv)
	}
}

// Given a directory but no job name, the version is reported and the job part
// says the name is not configured; the log is not searched under a guess.
func TestDoctorInstalledUnsetJobNameIsNamed(t *testing.T) {
	f := newInstallFixture(t)
	f.roleLibrary(t)
	f.src.RoleLibraryJob = ""
	f.log(t, "2026-11-08T10:00:00Z rl-job unchanged at "+sha1+"\n")
	out, failed := doctorRun(installedHost(t, f, sha1, nil))
	if failed != 0 {
		t.Fatalf("failed = %d\n%s", failed, out)
	}
	l := lineWith(out, "role-library")
	if !strings.Contains(l, sha1[:12]) || !strings.Contains(l, "job name not configured") {
		t.Errorf("role-library line = %q", l)
	}
}

// Given the two settings in the environment, the host input carries them, and
// with neither set it carries none and invents no default path.
func TestHostInstallSourceReadsOnlyTheNamedSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(roleLibraryDirEnv, "")
	t.Setenv(roleLibraryJobEnv, "")
	src := hostInstallSource()
	if src.RoleLibraryDir != "" || src.RoleLibraryJob != "" {
		t.Errorf("unset settings gave %+v", src)
	}
	t.Setenv(roleLibraryDirEnv, "/scratch/rl")
	t.Setenv(roleLibraryJobEnv, "rl-job")
	src = hostInstallSource()
	if src.RoleLibraryDir != "/scratch/rl" || src.RoleLibraryJob != "rl-job" {
		t.Errorf("set settings gave %+v", src)
	}
}
