package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// statInfo marks a line that reports a value and judges nothing.
const statInfo = "INFO"

// The two settings that say where the role-library install is and what the
// installed-copies job logs it as. Doctor has no default for either: a host
// that sets neither reads "not configured", never "not installed".
const (
	roleLibraryDirEnv = "MARVEL_ROLE_LIBRARY_DIR"
	roleLibraryJobEnv = "MARVEL_ROLE_LIBRARY_JOB"
)

// updateLogTail is how much of the end of the job log doctor reads. The log is
// one short line per component per run and grows without bound, so doctor reads
// only the end.
const updateLogTail = 64 * 1024

// installSource names where the installed-copies records live. Each path is
// read, never written. The zero value names nothing and doctor reads nothing.
type installSource struct {
	// RoleLibraryDir is the role-library install, a git work tree. Empty
	// means the host did not say where it is.
	RoleLibraryDir string
	// RoleLibraryJob is the component name the installed-copies job logs for
	// the role-library install. Empty means the host did not say.
	RoleLibraryJob string
	// DirectorHome is the director home whose bin/ links point into versions.
	DirectorHome string
	// UpdateLog is the installed-copies job's log.
	UpdateLog string
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// installRead is what reading one install record gave: a version, or why there
// is none. Unset means the host did not say where to look; absent means the
// install is not there; bad means it is there and could not be read.
type installRead struct {
	version string
	unset   bool
	absent  bool
	bad     string
}

// jobLogState is what reading the job log gave.
type jobLogState int

const (
	jobLogRead jobLogState = iota
	jobLogAbsent
	jobLogUnreadable
)

// jobLine is one parsed line of the job log.
type jobLine struct {
	when, verdict, detail string
}

// checkInstalled reports what each install records as its version, and the last
// line the installed-copies job wrote about it. It is a diagnostic: it compares
// nothing with the remote, so it has no notion of drift, and every outcome (an
// absent install, an unreadable record, an old job line) is a report line that
// never counts as a failure.
func checkInstalled(r *doctorReport, in doctorInput) {
	src := in.Installs
	if src == (installSource{}) {
		r.line(statSkip, "installs", "no install records named; none read", "")
		return
	}
	jobs, state := readJobLog(src.UpdateLog)
	emit := func(name, job string, rd installRead) {
		switch {
		case rd.unset:
			r.line(statSkip, name, "not configured; set "+roleLibraryDirEnv+" to read it", "")
		case rd.absent:
			r.line(statSkip, name, "not installed", "")
		case rd.bad != "":
			r.line(statWarn, name, "unreadable: "+rd.bad, "")
		default:
			r.line(statInfo, name, "at "+rd.version+"; "+jobSummary(job, jobs[job], state), "")
		}
	}
	emit("role-library", src.RoleLibraryJob, roleLibraryHead(in))
	emit("cast-launch", "director-cast-launch", directorVersion(src.DirectorHome, "cast-launch"))
	emit("director-mcp", "director-mcp", directorVersion(src.DirectorHome, "director-mcp"))
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func jobSummary(job string, l jobLine, state jobLogState) string {
	if job == "" {
		return "job name not configured; set " + roleLibraryJobEnv
	}
	switch state {
	case jobLogAbsent:
		return "job log absent"
	case jobLogUnreadable:
		return "job log unreadable"
	}
	if l.when == "" {
		return "no job line"
	}
	return "job: " + printable(l.when, 20) + " " + printable(l.verdict, 12) + " " + printable(l.detail, 60)
}

// printable cuts s to a short printable run, so a log line cannot flood the
// report or carry a terminal escape.
func printable(s string, limit int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r < ' ' || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		n++
		if n >= limit {
			break
		}
	}
	return b.String()
}

// roleLibraryHead reads the install's HEAD through the injected Run, so a test
// hands it a fake. No directory named is "not configured", which is not the same
// as a named directory with no .git, which is not installed.
func roleLibraryHead(in doctorInput) installRead {
	dir := in.Installs.RoleLibraryDir
	if dir == "" {
		return installRead{unset: true}
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return installRead{absent: true}
		}
		return installRead{bad: "the install could not be examined"}
	}
	out, err := in.Run("git", "-C", dir, "rev-parse", "HEAD")
	head := strings.TrimSpace(out)
	if err != nil || !fullSHA.MatchString(head) {
		return installRead{bad: "git did not give a HEAD"}
	}
	return installRead{version: short(head)}
}

// directorVersion reads which version directory bin/<name> points into, the
// record director-install keeps: bin/<name> -> versions/<name>/<sha>/<name>.
func directorVersion(home, name string) installRead {
	if home == "" {
		return installRead{absent: true}
	}
	link := filepath.Join(home, "bin", name)
	fi, err := os.Lstat(link)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return installRead{absent: true}
		}
		return installRead{bad: "the link could not be examined"}
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return installRead{bad: "not a link into a version directory"}
	}
	target, err := os.Readlink(link)
	if err != nil {
		return installRead{bad: "the link could not be read"}
	}
	v := filepath.Base(filepath.Dir(target))
	if !fullSHA.MatchString(v) {
		return installRead{bad: "the link does not point into a commit directory"}
	}
	return installRead{version: short(v)}
}

// readJobLog returns the last line per component from the end of the job log.
func readJobLog(path string) (map[string]jobLine, jobLogState) {
	out := map[string]jobLine{}
	if path == "" {
		return out, jobLogAbsent
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, jobLogAbsent
		}
		return out, jobLogUnreadable
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		return out, jobLogUnreadable
	}
	off := int64(0)
	if fi.Size() > updateLogTail {
		off = fi.Size() - updateLogTail
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return out, jobLogUnreadable
	}
	data, err := io.ReadAll(io.LimitReader(f, updateLogTail+1))
	if err != nil {
		return out, jobLogUnreadable
	}
	lines := strings.Split(string(data), "\n")
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line may be cut
	}
	for _, l := range lines {
		fields := strings.SplitN(l, " ", 4)
		if len(fields) < 3 {
			continue
		}
		jl := jobLine{when: fields[0], verdict: fields[2]}
		if len(fields) == 4 {
			jl.detail = fields[3]
		}
		out[fields[1]] = jl
	}
	return out, jobLogRead
}

// hostInstallSource names the records at their default places, the same ones
// the installed-copies job uses: DIRECTOR_HOME moves the director home, as it
// does for the job. The role-library install and its job name come only from the
// two settings above. With no home directory the director paths are empty.
func hostInstallSource() installSource {
	src := installSource{
		RoleLibraryDir: os.Getenv(roleLibraryDirEnv),
		RoleLibraryJob: os.Getenv(roleLibraryJobEnv),
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return src
	}
	dh := os.Getenv("DIRECTOR_HOME")
	if dh == "" {
		dh = filepath.Join(home, ".director")
	}
	src.DirectorHome = dh
	src.UpdateLog = filepath.Join(home, ".local", "state", "installed-copies", "update.log")
	return src
}
