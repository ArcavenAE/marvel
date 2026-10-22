package toolchain

import (
	"fmt"
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
	for _, name := range []string{"build-binaries", "release"} {
		if reasons := checkoutRefReasons(jobs[name]); len(reasons) > 0 {
			t.Errorf("job %s may not build or release github.sha:\n  %s", name, strings.Join(reasons, "\n  "))
		}
	}
}

// checkoutRefReasons returns nothing when every actions/checkout step of job
// has no ref, or ref ${{ github.sha }}, so that on a push the job works on the
// commit github.sha names, which is what COMMIT claims was built. A job with
// no checkout step is a reason too, since the premise would be unchecked.
func checkoutRefReasons(job ciJob) []string {
	var reasons []string
	checkouts := 0
	for _, step := range job.Steps {
		if !strings.HasPrefix(step.Uses, "actions/checkout@") {
			continue
		}
		checkouts++
		if ref, ok := step.With["ref"]; ok && ref != "${{ github.sha }}" {
			reasons = append(reasons, fmt.Sprintf("checkout sets ref %v, want none or ${{ github.sha }}", ref))
		}
	}
	if checkouts == 0 {
		reasons = append(reasons, "no actions/checkout step")
	}
	return reasons
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
// with its backslash continuations joined, names --target "$COMMIT". A shell
// comment ends its line, backslash included, so it neither continues the
// command nor counts as part of it.
func releaseCreateTargets(run string) bool {
	var logical []string
	var cur strings.Builder
	for _, line := range strings.Split(run, "\n") {
		code := stripShellComment(line)
		if code == line && strings.HasSuffix(code, "\\") {
			cur.WriteString(strings.TrimSuffix(code, "\\") + " ")
			continue
		}
		cur.WriteString(code)
		logical = append(logical, cur.String())
		cur.Reset()
	}
	logical = append(logical, cur.String())
	for _, line := range logical {
		if !strings.Contains(line, "gh release create") {
			continue
		}
		return strings.Contains(" "+strings.Join(strings.Fields(line), " ")+" ", ` --target "$COMMIT" `)
	}
	return false
}

// stripShellComment cuts line at the first # that starts a word: at the start
// of the line or after whitespace, outside single and double quotes and not
// escaped by a backslash. A # inside quotes or in the middle of a word, such
// as ${#ASSETS[@]} or a$#, is not a comment. Quote state is tracked per
// physical line, so a quote left open across lines is read as closed at the
// line's end.
func stripShellComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && quote != '\'':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
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
		{"--target commented out on its continuation line", step("${{ github.sha }}", "gh release create \"$TAG\" \\\n  --title t \\\n  # --target \"$COMMIT\" \\\n  --prerelease"), 1},
		{"--target commented out after the command", step("${{ github.sha }}", "gh release create \"$TAG\" # --target \"$COMMIT\""), 1},
		{"--target after a quoted # and a mid-word #", step("${{ github.sha }}", "gh release create \"$TAG\" --title \"a #1\" --notes b#2 --target \"$COMMIT\""), 0},
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

func TestCheckoutRefReasons(t *testing.T) {
	t.Parallel()

	co := func(with map[string]any) ciJobStep {
		return ciJobStep{Uses: "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", With: with}
	}
	for _, tc := range []struct {
		name string
		job  ciJob
		want int
	}{
		{"no ref", ciJob{Steps: []ciJobStep{co(map[string]any{"persist-credentials": false})}}, 0},
		{"no with", ciJob{Steps: []ciJobStep{co(nil)}}, 0},
		{"ref github.sha", ciJob{Steps: []ciJobStep{co(map[string]any{"ref": "${{ github.sha }}"})}}, 0},
		{"ref main", ciJob{Steps: []ciJobStep{co(map[string]any{"ref": "main"})}}, 1},
		{"second checkout on a ref", ciJob{Steps: []ciJobStep{co(nil), co(map[string]any{"ref": "${{ github.ref_name }}"})}}, 1},
		{"no checkout", ciJob{Steps: []ciJobStep{{Uses: "actions/setup-go@x"}}}, 1},
	} {
		if got := len(checkoutRefReasons(tc.job)); got != tc.want {
			t.Errorf("%s: %d reasons %v, want %d", tc.name, got, checkoutRefReasons(tc.job), tc.want)
		}
	}
}
