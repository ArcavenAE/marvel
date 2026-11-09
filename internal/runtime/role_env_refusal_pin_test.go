package runtime

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// pinnedRoleEnvProbe is every key whose treatment by mergeRoleEnv is pinned:
// true means a role may not set it. The set is the five identity credentials
// plus whatever api.BackendBearerEnv judges a literal bearer, as at the commit
// that moved the list into internal/api. The company decoder pins the same
// table in internal/company, so a change to either path's set fails a test.
var pinnedRoleEnvProbe = []struct {
	key     string
	refused bool
	warning string
}{
	{"MARVEL_HEARTBEAT_TOKEN", true, "marvel owns the credentials that prove a session's identity"},
	{"MARVEL_CREDENTIAL_HELPER", true, "marvel owns the credentials that prove a session's identity"},
	{"MARVEL_HEARTBEAT_TOKEN_FILE", true, "marvel owns the credentials that prove a session's identity"},
	{"DIRECTOR_NATS_USER", true, "marvel owns the credentials that prove a session's identity"},
	{"DIRECTOR_NATS_PASS", true, "marvel owns the credentials that prove a session's identity"},
	{"ANTHROPIC_API_KEY", true, "it carries a literal credential"},
	{"ANTHROPIC_AUTH_TOKEN", true, "it carries a literal credential"},
	{"CLAUDE_CODE_OAUTH_TOKEN", true, "it carries a literal credential"},
	{"AWS_BEARER_TOKEN_BEDROCK", true, "it carries a literal credential"},
	{"AWS_SECRET_ACCESS_KEY", true, "it carries a literal credential"},
	{"AWS_SESSION_TOKEN", true, "it carries a literal credential"},
	{"OPENAI_API_KEY", true, "it carries a literal credential"},
	{"SOME_VENDOR_AUTH_TOKEN", true, "it carries a literal credential"},
	{"X_BEARER_TOKEN_Y", true, "it carries a literal credential"},
	{"MARVEL_BACKEND_API_KEY_HELPER", false, ""},
	{"DIRECTOR_NATS_URL", false, ""},
	{"EDITOR", false, ""},
}

// captureLog sends the standard logger to the returned buffer until the test
// ends, then puts back the writer it found.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// The control for captureLog: after a subtest that captured the log has ended,
// the logger writes where it wrote before.
func TestCaptureLogPutsBackTheWriter(t *testing.T) {
	var sentinel bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&sentinel)
	t.Cleanup(func() { log.SetOutput(prev) })

	t.Run("capture", func(t *testing.T) { captureLog(t) })

	if log.Writer() != &sentinel {
		t.Errorf("the logger was left writing to %T, not to the writer it had", log.Writer())
	}
}

func TestMergeRoleEnvRefusesThePinnedCredentialSet(t *testing.T) {
	for _, p := range pinnedRoleEnvProbe {
		t.Run(p.key, func(t *testing.T) {
			buf := captureLog(t)

			ctx := testContext()
			ctx.Role.Runtime.Env = map[string]string{p.key: "a-literal-value"}
			env := mergeRoleEnv(ctx, map[string]string{})

			_, present := env[p.key]
			if present == p.refused {
				t.Fatalf("%s: carried = %v, want refused = %v", p.key, present, p.refused)
			}
			if p.refused && !strings.Contains(buf.String(), p.warning) {
				t.Errorf("%s: warning %q does not contain %q", p.key, buf.String(), p.warning)
			}
			if strings.Contains(buf.String(), "a-literal-value") {
				t.Errorf("%s: the warning echoed the value", p.key)
			}
		})
	}
}
