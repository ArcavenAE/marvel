package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// The container half of a seat whose placement resolved to container
// (docs/design/workspace-sandbox-shape.md, sections 5 and 7). Everything here is
// pure: nothing probes a daemon, reads the environment or starts a process, and
// nothing in the launch path calls it yet. The wiring into Manager.planLaunch,
// the image source and the refusal when no runtime can be had belong to the next
// step.

// ContainerSpec is everything the run command needs, passed in explicitly.
type ContainerSpec struct {
	// Runtime is the container CLI, as DetectContainerRuntime returned it.
	Runtime string
	// Image is the image to run.
	Image string
	// Name is the container name, from ContainerName.
	Name string
	// WorkDir is mounted at the same path inside and is the working directory.
	WorkDir string
	// HomeDir is the session's private harness home, mounted at the same path.
	// Empty means the harness has none.
	HomeDir string
	// EnvNames are the variables forwarded into the container, by name only: the
	// value comes from the pane's own environment, so a token never appears on a
	// command line.
	EnvNames []string
	// Command is the prepared command, as the adapter built it, shell text.
	Command string
}

// containerRuntimeCandidates is the lookup order. marvel detects whichever is
// installed (operator ruling 2026-10-14) and names none in config.
var containerRuntimeCandidates = []string{"docker", "podman"}

// DetectContainerRuntime returns the first installed container CLI, found by
// lookPath (exec.LookPath in production). It asks only whether a CLI is
// installed: whether the daemon behind it answers is a launch question.
func DetectContainerRuntime(lookPath func(string) (string, error)) (string, bool) {
	for _, name := range containerRuntimeCandidates {
		if p, err := lookPath(name); err == nil && p != "" {
			return p, true
		}
	}
	return "", false
}

var (
	containerNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	containerEnvRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// containerNameMax is the longest name ContainerName returns.
const containerNameMax = 63

// containerNamePrefix starts every name. `rm -f <name>` then cannot match a
// container by an all-hex ID prefix, and a name that lacks it is refused.
const containerNamePrefix = "marvel-"

// ContainerName builds a container name for one launch of one session. launch
// is any string that differs between launches of the same session (a
// generation, a start time): a relaunch after a restart or a shift then never
// collides with an orphan of the earlier one. The name is legal for the
// runtimes, at most 63 bytes, and carries a short hash of the raw inputs, so two
// keys that sanitize to the same text, or two long keys that differ only at the
// end, still get different names.
func ContainerName(sessionKey, launch string) string {
	sum := sha256.Sum256([]byte(sessionKey + "\x00" + launch))
	suffix := "-" + sanitizeContainerPart(launch) + "-" + hex.EncodeToString(sum[:4])
	const prefix = containerNamePrefix
	room := containerNameMax - len(prefix) - len(suffix)
	if room < 1 {
		// An absurd launch string: drop it from the readable part; the hash
		// still covers it.
		suffix = "-" + hex.EncodeToString(sum[:4])
		room = containerNameMax - len(prefix) - len(suffix)
	}
	key := sanitizeContainerPart(sessionKey)
	if len(key) > room {
		key = key[:room]
	}
	return prefix + key + suffix
}

func sanitizeContainerPart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// containerQuote single-quotes s unconditionally, escaping embedded single
// quotes. The host wrapper shell runs whatever it is handed, so every argument
// is quoted.
func containerQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func containerMountPath(what, p string) error {
	switch {
	case !filepath.IsAbs(p):
		return fmt.Errorf("container %s %q is not an absolute path", what, p)
	case strings.ContainsAny(p, ":\n\r\x00"):
		return fmt.Errorf("container %s %q holds a character a bind mount cannot carry", what, p)
	}
	return nil
}

// ContainerCommand builds the pane command that runs spec.Command under the
// container runtime. The pane runs the container client in the foreground, so
// the wrapper does three things the client alone does not:
//
//   - it removes any container of the same name before the run, so a relaunch
//     cannot collide with a leftover;
//   - it removes the container when the shell ends and when it is hung up or
//     terminated, which is what a pane kill, a deleted team, a shift drain and a
//     stop with teardown deliver (finding-051 measured the
//     container surviving all four);
//   - it passes the client's own exit status through.
//
// The removal on a hangup depends on the client exiting when its tty hangs up:
// a shell runs a trap only after its foreground command ends, so a client that
// ignores the hangup keeps the wrapper, and the removal, waiting. The tests
// cover the case where the hangup reaches the whole process group, which is
// what a pty hangup delivers; the real docker client's behavior under a real
// pty is unmeasured here.
//
// The harness runs under `sh -c` inside the image, so the image needs a sh.
func ContainerCommand(s ContainerSpec) (string, error) {
	switch {
	case s.Runtime == "":
		return "", fmt.Errorf("container runtime is empty")
	case s.Image == "":
		return "", fmt.Errorf("container image is empty")
	case !api.ValidContainerImage(s.Image):
		return "", fmt.Errorf("container image %q is not a plain image reference", s.Image)
	case !containerNameRE.MatchString(s.Name) || !strings.HasPrefix(s.Name, containerNamePrefix):
		return "", fmt.Errorf("container name %q is not a legal %s name", s.Name, containerNamePrefix+"...")
	case strings.TrimSpace(s.Command) == "":
		return "", fmt.Errorf("container command is empty")
	}
	if err := containerMountPath("workdir", s.WorkDir); err != nil {
		return "", err
	}
	if s.HomeDir != "" {
		if err := containerMountPath("home dir", s.HomeDir); err != nil {
			return "", err
		}
	}
	for _, e := range s.EnvNames {
		if !containerEnvRE.MatchString(e) {
			return "", fmt.Errorf("container env name %q is not a bare variable name", e)
		}
	}

	var run strings.Builder
	run.WriteString(`"$r" run --rm --init -it --name "$n"`)
	run.WriteString(" -w " + containerQuote(s.WorkDir))
	run.WriteString(" -v " + containerQuote(s.WorkDir+":"+s.WorkDir))
	if s.HomeDir != "" {
		run.WriteString(" -v " + containerQuote(s.HomeDir+":"+s.HomeDir))
	}
	for _, e := range s.EnvNames {
		run.WriteString(" -e " + e)
	}
	run.WriteString(" " + s.Image + " sh -c " + containerQuote(s.Command))

	rm := `"$r" rm -f "$n" >/dev/null 2>&1`
	return "n=" + containerQuote(s.Name) + "; r=" + containerQuote(s.Runtime) + "; " +
		"trap '" + rm + "' EXIT; " +
		"trap 'exit 129' HUP; trap 'exit 143' TERM; " +
		rm + "; " + run.String(), nil
}
