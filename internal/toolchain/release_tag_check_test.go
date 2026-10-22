package toolchain

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// notFound is what gh api prints for a 404: the response body on stdout, the
// error on stderr, and exit status 1.
const notFound = `echo '{"message":"Not Found","status":"404"}'; echo 'gh: Not Found (HTTP 404)' >&2; exit 1`

const (
	checkScript = "check-release-tag.sh"
	builtSHA    = "1111111111111111111111111111111111111111"
	otherSHA    = "2222222222222222222222222222222222222222"
	tagObjSHA   = "3333333333333333333333333333333333333333"
	tagObj2SHA  = "4444444444444444444444444444444444444444"
	releaseTag  = "alpha-20261022-050000-1111111"
	repoSlug    = "example/repo"
)

// Answers that make the stub fail instead of reading. failStderr exits 1 with
// a message on stderr only; failStatus exits 1 with a body on stdout and the
// message on stderr, as gh does for a 404; failEmpty exits 0 with no output.
const (
	failStderr = "!stderr"
	failStatus = "!status"
	failEmpty  = "!empty"
)

// stubGH is a gh that answers api calls from a table keyed by the request
// path, in the "<type> <sha>" form the script asks for with --jq, and logs
// every call. A path with no answer fails like a 404 (failStatus).
func stubGH(t *testing.T, answers map[string]string) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	var cases strings.Builder
	for path, answer := range answers {
		body := "echo '" + answer + "'"
		switch answer {
		case failStderr:
			body = "echo 'gh: connection reset' >&2; exit 1"
		case failStatus:
			body = notFound
		case failEmpty:
			body = "exit 0"
		}
		cases.WriteString("  " + path + ") " + body + " ;;\n")
	}
	script := "#!/bin/bash\necho \"$*\" >> '" + calls + "'\n" +
		"[[ \"$1\" == api ]] || exit 3\n" +
		"case \"$2\" in\n" + cases.String() + "  *) " + notFound + " ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, calls
}

func runCheckScript(t *testing.T, answers map[string]string, args ...string) (int, string, string) {
	t.Helper()
	bin, calls := stubGH(t, answers)
	cmd := exec.Command(filepath.Join("..", "..", "scripts", checkScript), args...)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "GITHUB_REPOSITORY=" + repoSlug, "HOME=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	log, _ := os.ReadFile(calls)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out), string(log)
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out), string(log)
	}
	t.Fatalf("run %s: %v", checkScript, err)
	return 0, "", ""
}

// The check reads where the release tag points and fails the step when that
// is not the built commit (marvel#1015). It reads only: a stub gh logs every
// call, and none may be anything but an api read.
func TestCheckReleaseTag(t *testing.T) {
	t.Parallel()

	ref := "repos/" + repoSlug + "/git/ref/tags/" + releaseTag
	tagObj := "repos/" + repoSlug + "/git/tags/" + tagObjSHA
	tagObj2 := "repos/" + repoSlug + "/git/tags/" + tagObj2SHA
	for _, tc := range []struct {
		name    string
		answers map[string]string
		args    []string
		exit    int
		out     []string // all must appear in the output
	}{
		{"lightweight tag at the built commit", map[string]string{ref: "commit " + builtSHA}, []string{releaseTag, builtSHA}, 0, nil},
		{
			"lightweight tag at another commit",
			map[string]string{ref: "commit " + otherSHA},
			[]string{releaseTag, builtSHA},
			1,
			[]string{releaseTag, otherSHA, builtSHA},
		},
		{
			"annotated tag peeling to the built commit",
			map[string]string{ref: "tag " + tagObjSHA, tagObj: "commit " + builtSHA},
			[]string{releaseTag, builtSHA},
			0, nil,
		},
		{
			"annotated tag peeling to another commit",
			map[string]string{ref: "tag " + tagObjSHA, tagObj: "commit " + otherSHA},
			[]string{releaseTag, builtSHA},
			1,
			[]string{releaseTag, otherSHA, builtSHA},
		},
		{
			"annotated tag of an annotated tag, to the built commit",
			map[string]string{ref: "tag " + tagObjSHA, tagObj: "tag " + tagObj2SHA, tagObj2: "commit " + builtSHA},
			[]string{releaseTag, builtSHA},
			0, nil,
		},
		{"tag that points at a tree", map[string]string{ref: "tree " + otherSHA}, []string{releaseTag, builtSHA}, 1, []string{releaseTag, builtSHA}},
		{"tag that does not exist", map[string]string{}, []string{releaseTag, builtSHA}, 1, []string{"could not read", releaseTag, builtSHA}},
		{
			"ref read fails: stderr only",
			map[string]string{ref: failStderr},
			[]string{releaseTag, builtSHA},
			1,
			[]string{"could not read", releaseTag, builtSHA},
		},
		{
			"ref read fails: nonzero status with a body",
			map[string]string{ref: failStatus},
			[]string{releaseTag, builtSHA},
			1,
			[]string{"could not read", releaseTag, builtSHA},
		},
		{
			"ref read fails: empty output",
			map[string]string{ref: failEmpty},
			[]string{releaseTag, builtSHA},
			1,
			[]string{"could not read", releaseTag, builtSHA},
		},
		{
			"tag object read fails: stderr only",
			map[string]string{ref: "tag " + tagObjSHA, tagObj: failStderr},
			[]string{releaseTag, builtSHA},
			1,
			[]string{"could not read", releaseTag, builtSHA},
		},
		{
			"tag object read fails: nonzero status with a body",
			map[string]string{ref: "tag " + tagObjSHA, tagObj: failStatus},
			[]string{releaseTag, builtSHA},
			1,
			[]string{"could not read", releaseTag, builtSHA},
		},
		{
			"tag object read fails: empty output",
			map[string]string{ref: "tag " + tagObjSHA, tagObj: failEmpty},
			[]string{releaseTag, builtSHA},
			1,
			[]string{"could not read", releaseTag, builtSHA},
		},
		{"no arguments", map[string]string{ref: "commit " + builtSHA}, nil, 2, nil},
	} {
		exit, out, calls := runCheckScript(t, tc.answers, tc.args...)
		if exit != tc.exit {
			t.Errorf("%s: exit %d, want %d; output:\n%s", tc.name, exit, tc.exit, out)
		}
		if slices.Contains(tc.out, "could not read") {
			named := 0
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, releaseTag) && strings.Contains(line, builtSHA) {
					named++
				}
			}
			if named != 1 {
				t.Errorf("%s: %d output lines name the tag and the built commit, want 1:\n%s", tc.name, named, out)
			}
		}
		for _, want := range tc.out {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output lacks %q:\n%s", tc.name, want, out)
			}
		}
		for _, call := range strings.Split(strings.TrimSpace(calls), "\n") {
			if call != "" && (!strings.HasPrefix(call, "api repos/"+repoSlug+"/git/") || strings.Contains(call, "-X") || strings.Contains(call, "DELETE")) {
				t.Errorf("%s: gh call %q is not a read of the git API", tc.name, call)
			}
		}
	}
}

// No command of the script, apart from comments and echo lines, may delete,
// push or write through the API, so a mismatch leaves the tag and the release
// for a person to act on.
func TestCheckReleaseTagDeletesNothing(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", checkScript))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "echo ") {
			continue
		}
		for _, word := range []string{"delete", "DELETE", "gh release", "git push", "rm ", "-X", "--method"} {
			if strings.Contains(line, word) {
				t.Errorf("%s runs %q in %q; it may only read", checkScript, word, line)
			}
		}
	}
}

// TestReleaseJobChecksTheTag requires the release job to run the check right
// after gh release create, with TAG and COMMIT as the create step has them,
// and with nothing that lets a failure pass.
func TestReleaseJobChecksTheTag(t *testing.T) {
	t.Parallel()

	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if reasons := tagCheckReasons(jobs["release"]); len(reasons) > 0 {
		t.Errorf("the release job does not check its tag:\n  %s", strings.Join(reasons, "\n  "))
	}
}

// tagCheckReasons returns nothing when the step after the one running gh
// release create runs scripts/check-release-tag.sh "$TAG" "$COMMIT" with TAG
// and COMMIT the create step's, a GH_TOKEN, and no if, continue-on-error or
// shell operator that lets it pass.
func tagCheckReasons(job ciJob) []string {
	for i, step := range job.Steps {
		if !strings.Contains(step.Run, "gh release create") {
			continue
		}
		if i+1 == len(job.Steps) {
			return []string{"no step after gh release create"}
		}
		next := job.Steps[i+1]
		var reasons []string
		if strings.TrimSpace(next.Run) != `scripts/`+checkScript+` "$TAG" "$COMMIT"` {
			reasons = append(reasons, "step "+next.Name+" runs "+strings.TrimSpace(next.Run)+", want scripts/"+checkScript+` "$TAG" "$COMMIT"`)
		}
		for _, k := range []string{"TAG", "COMMIT"} {
			if next.Env[k] != step.Env[k] || next.Env[k] == "" {
				reasons = append(reasons, "step "+next.Name+": "+k+" is "+next.Env[k]+", want the create step's "+step.Env[k])
			}
		}
		if next.Env["GH_TOKEN"] == "" {
			reasons = append(reasons, "step "+next.Name+": no GH_TOKEN")
		}
		if next.If != "" || next.ContinueOnError != nil {
			reasons = append(reasons, "step "+next.Name+" has an if or continue-on-error")
		}
		return reasons
	}
	return []string{"no step runs gh release create"}
}

func TestTagCheckReasons(t *testing.T) {
	t.Parallel()

	create := ciJobStep{Name: "Create", Run: "gh release create x", Env: map[string]string{"TAG": "t", "COMMIT": "c"}}
	check := func(mut func(*ciJobStep)) ciJob {
		s := ciJobStep{
			Name: "Check", Run: `scripts/` + checkScript + ` "$TAG" "$COMMIT"`,
			Env: map[string]string{"TAG": "t", "COMMIT": "c", "GH_TOKEN": "x"},
		}
		if mut != nil {
			mut(&s)
		}
		return ciJob{Steps: []ciJobStep{create, s}}
	}
	for _, tc := range []struct {
		name string
		job  ciJob
		want int
	}{
		{"the check follows the create", check(nil), 0},
		{"no step after the create", ciJob{Steps: []ciJobStep{create}}, 1},
		{"a different script", check(func(s *ciJobStep) { s.Run = "true" }), 1},
		{"|| true", check(func(s *ciJobStep) { s.Run += " || true" }), 1},
		{"COMMIT not the create step's", check(func(s *ciJobStep) { s.Env["COMMIT"] = "other" }), 1},
		{"no GH_TOKEN", check(func(s *ciJobStep) { delete(s.Env, "GH_TOKEN") }), 1},
		{"continue-on-error", check(func(s *ciJobStep) { s.ContinueOnError = true }), 1},
		{"an if", check(func(s *ciJobStep) { s.If = "always()" }), 1},
		{"another step between", ciJob{Steps: []ciJobStep{create, {Name: "Other", Run: "echo"}, check(nil).Steps[1]}}, 2},
	} {
		if got := len(tagCheckReasons(tc.job)); got < 1 != (tc.want == 0) {
			t.Errorf("%s: %d reasons %v, want %d", tc.name, got, tagCheckReasons(tc.job), tc.want)
		}
	}
}
