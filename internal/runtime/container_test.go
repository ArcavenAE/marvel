package runtime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// DetectContainerRuntime finds whatever is installed, docker first, then podman
// (the operator ruled on 2026-10-14 that marvel detects the runtime rather than
// naming one in config). Nothing is probed beyond the lookup: whether the daemon
// behind the CLI answers is the launch refusal's business, not this function's.
func TestDetectContainerRuntime(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, h := range names {
				if n == h {
					return "/opt/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	for name, tc := range map[string]struct {
		lookup func(string) (string, error)
		want   string
		found  bool
	}{
		"docker only":            {has("docker"), "/opt/bin/docker", true},
		"podman only":            {has("podman"), "/opt/bin/podman", true},
		"both prefers docker":    {has("podman", "docker"), "/opt/bin/docker", true},
		"neither":                {has(), "", false},
		"orb alone is not a one": {has("orb"), "", false},
		// exec.LookPath can return a path together with an error (found, not
		// executable); that docker is not usable and podman is.
		"a path with an error is skipped": {func(n string) (string, error) {
			if n == "docker" {
				return "/opt/bin/docker", errors.New("permission denied")
			}
			return "/opt/bin/" + n, nil
		}, "/opt/bin/podman", true},
	} {
		got, ok := DetectContainerRuntime(tc.lookup)
		if got != tc.want || ok != tc.found {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", name, got, ok, tc.want, tc.found)
		}
	}
}

var legalContainerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// A name is legal for the runtimes, differs per launch of the same session (a
// restart or a shift never collides with an orphan), and is stable for the same
// inputs.
func TestContainerNameIsLegalAndUniquePerLaunch(t *testing.T) {
	key := "pt8k/probe/seat-g1-0"
	a := ContainerName(key, "1")
	b := ContainerName(key, "2")
	if !legalContainerName.MatchString(a) || !legalContainerName.MatchString(b) {
		t.Fatalf("illegal names %q %q", a, b)
	}
	if a == b {
		t.Errorf("two launches share the name %q", a)
	}
	if again := ContainerName(key, "1"); again != a {
		t.Errorf("not stable: %q then %q", a, again)
	}
	if o := ContainerName("pt8k/probe/seat-g1-1", "1"); o == a {
		t.Errorf("two sessions share the name %q", a)
	}
	for _, hostile := range []string{"ws/te am/ro$le", "a/b/../c", "x;rm -rf /", "naïve/café/seat", strings.Repeat("long/", 40)} {
		n := ContainerName(hostile, "7")
		if !legalContainerName.MatchString(n) {
			t.Errorf("ContainerName(%q) = %q is not a legal name", hostile, n)
		}
		if len(n) > 63 {
			t.Errorf("ContainerName(%q) is %d bytes, want at most 63", hostile, len(n))
		}
	}
	// Inputs that sanitize to the same text still get different names.
	if ContainerName("ws/te am/seat", "1") == ContainerName("ws/te-am/seat", "1") {
		t.Error("two keys that sanitize alike share a name")
	}
	if ContainerName(key, "a b") == ContainerName(key, "a-b") {
		t.Error("two launches that sanitize alike share a name")
	}
	// Truncation must not merge two different long keys.
	long := strings.Repeat("segment/", 20)
	if ContainerName(long+"a", "1") == ContainerName(long+"b", "1") {
		t.Error("two long keys that differ only at the end share a name")
	}
}

func goodSpec() ContainerSpec {
	return ContainerSpec{
		Runtime:  "/opt/bin/docker",
		Image:    "registry.example/marvel-pi:1",
		Name:     "marvel-ws-team-seat-g1-0-1",
		WorkDir:  "/work/repo",
		HomeDir:  "/tmp/marvel-h-abc/def",
		EnvNames: []string{"MARVEL_SESSION", "MARVEL_HEARTBEAT_TOKEN"},
		Command:  "pi --flag 'a b'",
	}
}

// The command names the run once, with the flags that keep the container tied
// to the pane, the mounts at the same path inside as outside, and each env name
// bare so no value reaches a command line.
func TestContainerCommandShape(t *testing.T) {
	cmd, err := ContainerCommand(goodSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		" run --rm --init -it ",
		"--name ",
		" -w '/work/repo' ",
		" -v '/work/repo:/work/repo' ",
		" -v '/tmp/marvel-h-abc/def:/tmp/marvel-h-abc/def' ",
		" -e MARVEL_SESSION ",
		" -e MARVEL_HEARTBEAT_TOKEN ",
		" registry.example/marvel-pi:1 sh -c ",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q:\n%s", want, cmd)
		}
	}
	if strings.Contains(cmd, "MARVEL_SESSION=") || strings.Contains(cmd, "TOKEN=") {
		t.Errorf("an env value is on the command line:\n%s", cmd)
	}
	if !strings.Contains(cmd, "pi --flag") {
		t.Errorf("the prepared command is not carried:\n%s", cmd)
	}
	// No home dir means no home mount.
	s := goodSpec()
	s.HomeDir = ""
	cmd, err = ContainerCommand(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(cmd, " -v ") != 1 {
		t.Errorf("want only the workdir mount without a home dir:\n%s", cmd)
	}
}

func TestContainerCommandRefusesBadInput(t *testing.T) {
	for name, edit := range map[string]func(*ContainerSpec){
		"no runtime":               func(s *ContainerSpec) { s.Runtime = "" },
		"no image":                 func(s *ContainerSpec) { s.Image = "" },
		"no name":                  func(s *ContainerSpec) { s.Name = "" },
		"no command":               func(s *ContainerSpec) { s.Command = "" },
		"relative workdir":         func(s *ContainerSpec) { s.WorkDir = "repo" },
		"empty workdir":            func(s *ContainerSpec) { s.WorkDir = "" },
		"relative home":            func(s *ContainerSpec) { s.HomeDir = "home" },
		"colon in workdir":         func(s *ContainerSpec) { s.WorkDir = "/work/a:b" },
		"colon in home":            func(s *ContainerSpec) { s.HomeDir = "/h/a:b" },
		"illegal name":             func(s *ContainerSpec) { s.Name = "bad name" },
		"name starts with dash":    func(s *ContainerSpec) { s.Name = "-rm" },
		"prefixed name with space": func(s *ContainerSpec) { s.Name = "marvel-a b" },
		"prefixed name with ;":     func(s *ContainerSpec) { s.Name = "marvel-a;b" },
		"prefixed name with $()":   func(s *ContainerSpec) { s.Name = "marvel-$(x)" },
		"prefixed name with nl":    func(s *ContainerSpec) { s.Name = "marvel-a\nb" },
		"image starts with dash":   func(s *ContainerSpec) { s.Image = "--privileged" },
		"image with a space":       func(s *ContainerSpec) { s.Image = "a b" },
		"env name with equals":     func(s *ContainerSpec) { s.EnvNames = []string{"A=1"} },
		"env name with a space":    func(s *ContainerSpec) { s.EnvNames = []string{"A B"} },
		"env name starts digit":    func(s *ContainerSpec) { s.EnvNames = []string{"1A"} },
		"env name empty":           func(s *ContainerSpec) { s.EnvNames = []string{""} },
		"env name shell injection": func(s *ContainerSpec) { s.EnvNames = []string{"A;rm"} },
	} {
		s := goodSpec()
		edit(&s)
		if cmd, err := ContainerCommand(s); err == nil {
			t.Errorf("%s: want an error, got a command:\n%s", name, cmd)
		}
	}
}

// fakeRuntime writes a script standing for the container CLI. It logs every call
// to log, and `run` waits for runSecs seconds (or exits with the code in
// $FAKE_RUN_EXIT when it is 0).
func fakeRuntime(t *testing.T, runSecs string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "fakedocker")
	log = filepath.Join(dir, "calls.log")
	// One record per call, with newline, tab and carriage return in an argument
	// written as \001, \002 and \003, so an argument holding one cannot split
	// the record (see logForm).
	script := "#!/bin/sh\n{ printf '%s' \"$1 $*\" | tr '\\n\\t\\r' '\\001\\002\\003'; echo; } >> '" + log + "'\n" +
		"if [ \"$1\" = run ]; then\n  for a; do last=$a; done; printf '%s' \"$last\" > '" + log + ".last'\n  if [ '" + runSecs + "' != 0 ]; then sleep " + runSecs + "; fi\n  exit ${FAKE_RUN_EXIT:-0}\nfi\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// logForm is how fakeRuntime records an argument: newline, tab and carriage
// return become \001, \002 and \003.
func logForm(s string) string {
	return strings.NewReplacer("\n", "\x01", "\t", "\x02", "\r", "\x03").Replace(s)
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func shells(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, s := range []string{"sh", "bash", "zsh", "dash"} {
		if _, err := exec.LookPath(s); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// The container is removed when the client ends, whatever the exit path, and the
// harness's exit code is the pane's.
func TestContainerCommandRemovesTheContainerOnNormalExit(t *testing.T) {
	for _, sh := range shells(t) {
		bin, log := fakeRuntime(t, "0")
		spec := goodSpec()
		spec.Runtime = bin
		cmd, err := ContainerCommand(spec)
		if err != nil {
			t.Fatal(err)
		}
		c := exec.Command(sh, "-c", cmd)
		c.Env = append(os.Environ(), "FAKE_RUN_EXIT=7")
		err = c.Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 7 {
			t.Errorf("%s: exit = %v, want the container client's own code 7", sh, err)
		}
		got := calls(t, log)
		if len(got) < 2 || !strings.HasPrefix(got[len(got)-1], "rm -f "+spec.Name) && !strings.Contains(got[len(got)-1], "rm -f "+spec.Name) {
			t.Errorf("%s: the last runtime call is not 'rm -f %s': %v", sh, spec.Name, got)
		}
	}
}

// A killed pane is the case finding-051 measured: tmux hangs the pty up, the
// client dies, and the container used to keep running. The wrapper's trap must
// remove it, for HUP and for TERM, which cover a pane kill, a deleted team, a
// shift drain and a stop with teardown.
func TestContainerCommandRemovesTheContainerWhenThePaneIsKilled(t *testing.T) {
	for _, sh := range shells(t) {
		for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT} {
			bin, log := fakeRuntime(t, "30")
			spec := goodSpec()
			spec.Runtime = bin
			cmd, err := ContainerCommand(spec)
			if err != nil {
				t.Fatal(err)
			}
			c := exec.Command(sh, "-c", cmd)
			c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			// Wait until the fake `run` has started, then signal the group the
			// way a pty hangup does.
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) && len(calls(t, log)) < 2 {
				time.Sleep(20 * time.Millisecond)
			}
			if err := syscall.Kill(-c.Process.Pid, sig); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- c.Wait() }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
				t.Errorf("%s %v: the wrapper did not exit", sh, sig)
				continue
			}
			got := calls(t, log)
			last := got[len(got)-1]
			if !strings.Contains(last, "rm -f "+spec.Name) {
				t.Errorf("%s %v: the container was not removed; calls: %v", sh, sig, got)
			}
		}
	}
}

// Before the run, a leftover of the same name is removed, so a relaunch cannot
// collide with an orphan from an earlier teardown that the trap could not reach.
func TestContainerCommandClearsALeftoverBeforeTheRun(t *testing.T) {
	bin, log := fakeRuntime(t, "0")
	spec := goodSpec()
	spec.Runtime = bin
	cmd, _ := ContainerCommand(spec)
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	runAt, firstRm := -1, -1
	for i, c := range got {
		if strings.HasPrefix(c, "run ") && runAt < 0 {
			runAt = i
		}
		if strings.Contains(c, "rm -f "+spec.Name) && firstRm < 0 {
			firstRm = i
		}
	}
	if runAt < 0 || firstRm < 0 || firstRm > runAt {
		t.Errorf("want an rm -f before the run: %v", got)
	}
}

// Hostile text in the prepared command or the paths cannot break out of the
// wrapper: a quote or a substitution reaches the container as text.
func TestContainerCommandQuotesTheCommand(t *testing.T) {
	bin, log := fakeRuntime(t, "0")
	spec := goodSpec()
	spec.Runtime = bin
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	spec.Command = "echo 'x'; touch " + marker + "; echo `id` $(id)"
	cmd, err := ContainerCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		t.Fatal(err)
	}
	run := ""
	for _, c := range calls(t, log) {
		if strings.HasPrefix(c, "run ") {
			run = c
		}
	}
	if !strings.Contains(run, "`id`") || !strings.Contains(run, "$(id)") {
		t.Errorf("the prepared command did not arrive as one literal argument: %q", run)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the prepared command ran in the wrapper's shell")
	}
}

// Whitespace that splits a shell word, newline and tab included, must not leave
// the quotes: a command, a path or a runtime carrying one reaches the host
// wrapper shell, which runs the text it is given. The marker is created only if
// the host shell ran the second line.
func TestContainerCommandQuotesNewlineAndTabOnTheHost(t *testing.T) {
	for _, sh := range shells(t) {
		for name, payload := range map[string]string{
			"newline":      "true\ntouch\tMARKER",
			"tab":          "true\ttouch\tMARKER",
			"carriage":     "true\rtouch\tMARKER",
			"semicolon":    "true;touch\tMARKER",
			"quote":        "true';touch MARKER;'",
			"substitution": "true$(touch MARKER)",
			"backtick":     "true`touch MARKER`",
		} {
			dir := t.TempDir()
			marker := filepath.Join(dir, "marker")
			bin, log := fakeRuntime(t, "0")
			spec := goodSpec()
			spec.Runtime = bin
			spec.Command = strings.ReplaceAll(payload, "MARKER", marker)
			cmd, err := ContainerCommand(spec)
			if err != nil {
				t.Fatal(err)
			}
			if err := exec.Command(sh, "-c", cmd).Run(); err != nil {
				t.Fatalf("%s %s: %v", sh, name, err)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Errorf("%s %s: the host wrapper shell ran the command text", sh, name)
			}
			last, err := os.ReadFile(log + ".last")
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.ReplaceAll(payload, "MARKER", marker); string(last) != want {
				t.Errorf("%s %s: the container got %q as its command argument, want %q", sh, name, last, want)
			}
		}
	}
}

// A path or the runtime with a tab in it, and no space, is one word too: the old
// quoting let that through.
func TestContainerCommandQuotesPathsAndRuntimeWithWhitespace(t *testing.T) {
	bin, log := fakeRuntime(t, "0")
	dir := t.TempDir()
	spacedBin := filepath.Join(dir, "my\tdocker")
	if err := os.Symlink(bin, spacedBin); err != nil {
		t.Fatal(err)
	}
	spec := goodSpec()
	spec.Runtime = spacedBin
	spec.WorkDir = "/work/a\tb\tc"
	spec.HomeDir = "/home/h\tx"
	cmd, err := ContainerCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		t.Fatal(err)
	}
	var run string
	for _, c := range calls(t, log) {
		if strings.HasPrefix(c, "run ") {
			run = c
		}
	}
	for _, want := range []string{"-w /work/a\tb\tc", "-v /work/a\tb\tc:/work/a\tb\tc", "-v /home/h\tx:/home/h\tx"} {
		if !strings.Contains(run, logForm(want)) {
			t.Errorf("run line lacks %q: %q", want, run)
		}
	}
}

// A very long launch string cannot push the name past the limit or make two
// launches collide; the hash still covers what the readable part drops.
func TestContainerNameWithAVeryLongLaunch(t *testing.T) {
	long := strings.Repeat("x", 200)
	a := ContainerName("ws/team/seat", long+"a")
	b := ContainerName("ws/team/seat", long+"b")
	for _, n := range []string{a, b} {
		if !legalContainerName.MatchString(n) || len(n) > 63 {
			t.Errorf("name %q is illegal or longer than 63 bytes (%d)", n, len(n))
		}
	}
	if a == b {
		t.Error("two long launches that differ at the end share a name")
	}
}

// Every name carries the marvel- prefix, so `rm -f <name>` can never match a
// container by an all-hex ID prefix, and a name that does not start with it is
// refused.
func TestContainerNameAlwaysCarriesThePrefixAndTheSpecRequiresIt(t *testing.T) {
	for _, k := range []string{"deadbeef", "0123", "ws/team/seat"} {
		if n := ContainerName(k, "1"); !strings.HasPrefix(n, "marvel-") {
			t.Errorf("ContainerName(%q) = %q lacks the marvel- prefix", k, n)
		}
	}
	for _, bad := range []string{"deadbeef", "seat-1", "Marvel-x", "xmarvel-1"} {
		s := goodSpec()
		s.Name = bad
		if cmd, err := ContainerCommand(s); err == nil {
			t.Errorf("name %q accepted:\n%s", bad, cmd)
		}
	}
}
