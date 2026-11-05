package view

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A remote that starts with a dash is a remote name, not a git option. The
// control runs git without the "--" and shows --upload-pack=<program> would
// start the program, so a quiet marker for Fetch means the separator held.
func TestFetchTreatsADashRemoteAsAName(t *testing.T) {
	scratch := t.TempDir()
	marker := filepath.Join(scratch, "marker")
	prog := filepath.Join(scratch, "up.sh")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	remote := "--upload-pack=" + prog
	env := append(cleanGitEnv(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")

	ctrlMirror := filepath.Join(scratch, "control.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", ctrlMirror).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	ctrl := exec.Command("git", "--git-dir", ctrlMirror, "fetch", "-q", "--no-tags", remote, "+refs/heads/main:"+viewRef)
	ctrl.Env = env
	_ = ctrl.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control: git fetch without the separator did not run the program (%v); the test cannot tell anything", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	if err := (ExecGit{}).Fetch(context.Background(), filepath.Join(scratch, "mirror.git"), remote, "main"); err == nil {
		t.Fatal("Fetch of a dash remote returned no error")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Fetch ran the program named by a remote that starts with a dash")
	}
}
