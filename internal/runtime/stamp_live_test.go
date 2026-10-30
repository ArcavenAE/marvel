//go:build harness

package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// aae-orc-mdr0n S7: the installed claude, started with the environment marvel
// built for a launch, reports the stamp as resource attributes. It runs only
// under the harness tag (go test -tags harness), because it needs the claude
// binary, and it fails, never skips, when claude is absent.
//
// It runs claude with the console exporter into a file, under a scratch HOME,
// with no login. Claude exits at once with "Not logged in" and still exports
// its resource attributes; the exit status is not what is read. Run it under a
// profile that denies the network, so a stray login on the host cannot reach a
// model.
func TestClaudeTelemetryCarriesTheStamp(t *testing.T) {
	t.Setenv(resourceAttributesEnv, "")
	ctx := claudeCtx("claude")
	ctx.Cluster = "c1"
	r := prepareStamped(t, ctx)
	got := claudeTelemetry(t, r.Env[resourceAttributesEnv])
	for _, want := range []string{
		`"marvel.workspace": "acme"`,
		`"marvel.team": "squad"`,
		`"marvel.role": "worker"`,
		`"marvel.session": "squad-worker-g1-0"`,
		`"marvel.seat": "marvel://c1/acme/squad/worker-0"`,
		`"marvel.cluster": "c1"`,
		`"marvel.system_prompt.sha256": "` + r.SystemPromptSha256 + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("claude's telemetry lacks %s\n%s", want, got)
		}
	}
}

// The harness decodes the percent-encoded values marvel writes, so a value with
// a space, a comma and an equals sign arrives as written. A workspace name like
// this is not a valid seat URI part, so no seat is stamped; the other fields are.
func TestClaudeTelemetryDecodesWhatMarvelEncodes(t *testing.T) {
	t.Setenv(resourceAttributesEnv, "")
	ctx := claudeCtx("claude")
	ctx.Workspace.Name = "a b,c=d"
	ctx.Session.Workspace = "a b,c=d"
	r := prepareStamped(t, ctx)
	got := claudeTelemetry(t, r.Env[resourceAttributesEnv])
	if want := `"marvel.workspace": "a b,c=d"`; !strings.Contains(got, want) {
		t.Errorf("claude's telemetry lacks %s\n%s", want, got)
	}
}

// claudeTelemetry runs the installed claude with the console exporter into a
// file and returns what it wrote.
func claudeTelemetry(t *testing.T, attrs string) string {
	t.Helper()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("claude is not installed, and this test needs the harness binary: %v", err)
	}
	out := filepath.Join(t.TempDir(), "telemetry.out")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	run, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(run, claude, "-p", "say hi")
	cmd.Env = []string{
		"HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(),
		"PATH=" + os.Getenv("PATH"),
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"OTEL_METRICS_EXPORTER=console",
		"OTEL_LOGS_EXPORTER=console",
		"OTEL_METRIC_EXPORT_INTERVAL=1000",
		"OTEL_LOGS_EXPORT_INTERVAL=1000",
		resourceAttributesEnv + "=" + attrs,
	}
	cmd.Stdout, cmd.Stderr = f, f
	_ = cmd.Run()

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
