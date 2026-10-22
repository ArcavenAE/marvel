package toolchain

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseTagsTheBuiltCommit holds the alpha release step to creating its
// tag at the commit the job built. Without --target, gh release create tags
// the branch tip at the moment the step runs, so a push that lands in between
// leaves a tag whose suffix and binaries name one commit and whose tree is
// another's. build-binaries and release both check out the push event's sha,
// so github.sha, which the step already records as COMMIT, is the built one.
func TestReleaseTagsTheBuiltCommit(t *testing.T) {
	t.Parallel()

	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if reasons := releaseTargetReasons(jobs["release"]); len(reasons) > 0 {
		t.Errorf("the release job does not tag the built commit:\n  %s", strings.Join(reasons, "\n  "))
	}
}

// releaseTargetReasons returns nothing when exactly one step of job runs gh
// release create with --target "$COMMIT" and sets COMMIT to github.sha.
func releaseTargetReasons(job ciJob) []string {
	var reasons []string
	creates := 0
	for _, step := range job.Steps {
		if !strings.Contains(step.Run, "gh release create") {
			continue
		}
		creates++
		if got := step.Env["COMMIT"]; got != "${{ github.sha }}" {
			reasons = append(reasons, "step "+step.Name+": COMMIT is "+got+", want ${{ github.sha }}")
		}
		if !releaseCreateTargets(step.Run) {
			reasons = append(reasons, "step "+step.Name+": gh release create carries no --target \"$COMMIT\"")
		}
	}
	if creates != 1 {
		reasons = append(reasons, "want one step that runs gh release create")
	}
	return reasons
}

// releaseCreateTargets reports whether the gh release create command in run,
// with its backslash continuations joined, names --target "$COMMIT".
func releaseCreateTargets(run string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(run, "\\\n", " "), "\n") {
		if !strings.Contains(line, "gh release create") {
			continue
		}
		return strings.Contains(" "+strings.Join(strings.Fields(line), " ")+" ", ` --target "$COMMIT" `)
	}
	return false
}

func TestReleaseTargetReasons(t *testing.T) {
	t.Parallel()

	const create = "gh release create \"$TAG\" \\\n  --title t \\\n  --target \"$COMMIT\" \\\n  --prerelease"
	step := func(env, run string) ciJob {
		return ciJob{Steps: []ciJobStep{{Name: "Create", Env: map[string]string{"COMMIT": env}, Run: run}}}
	}
	for _, tc := range []struct {
		name string
		job  ciJob
		want int
	}{
		{"target and built sha", step("${{ github.sha }}", create), 0},
		{"no --target", step("${{ github.sha }}", "gh release create \"$TAG\" \\\n  --prerelease"), 1},
		{"target a branch name", step("${{ github.sha }}", "gh release create \"$TAG\" --target main"), 1},
		{"target the literal word COMMIT", step("${{ github.sha }}", "gh release create \"$TAG\" --target COMMIT"), 1},
		{"COMMIT not the built sha", step("${{ github.ref_name }}", create), 1},
		{"COMMIT unset", ciJob{Steps: []ciJobStep{{Name: "Create", Run: create}}}, 1},
		{"no create step", ciJob{Steps: []ciJobStep{{Name: "Other", Run: "echo hi"}}}, 1},
		{"two create steps", ciJob{Steps: []ciJobStep{
			{Name: "A", Env: map[string]string{"COMMIT": "${{ github.sha }}"}, Run: create},
			{Name: "B", Env: map[string]string{"COMMIT": "${{ github.sha }}"}, Run: create},
		}}, 1},
	} {
		if got := len(releaseTargetReasons(tc.job)); got != tc.want {
			t.Errorf("%s: %d reasons %v, want %d", tc.name, got, releaseTargetReasons(tc.job), tc.want)
		}
	}
}
