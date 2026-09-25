package api

import (
	"strings"
	"testing"
)

func overlayEnv(t *testing.T, settings map[string]any) map[string]string {
	t.Helper()
	raw, ok := settings["env"].(map[string]string)
	if !ok {
		t.Fatalf("settings has no env block: %#v", settings)
	}
	return raw
}

func TestBuildBackendOverlayBedrock(t *testing.T) {
	t.Parallel()
	settings, err := BuildBackendOverlay(BackendOverlay{Mode: BackendBedrock})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	env := overlayEnv(t, settings)
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("bedrock selector = %q, want 1", env["CLAUDE_CODE_USE_BEDROCK"])
	}
	// Every competitor must be pinned OFF, or an ambient selector outranks the
	// chosen one (Bedrock/Foundry outrank Platform-on-AWS).
	for _, competitor := range []string{"CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_MANTLE", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_GATEWAY"} {
		if env[competitor] != "0" {
			t.Errorf("competitor %s = %q, want 0", competitor, env[competitor])
		}
	}
	if got, ok := env["ANTHROPIC_API_KEY"]; !ok || got != "" {
		t.Errorf("ANTHROPIC_API_KEY = %q (present=%v), want empty and present", got, ok)
	}
	// The overlay's env resolves to bedrock through the classifier, matching the
	// intent recorded on the session.
	if got := ResolveBackend(lookupFrom(env)); got != BackendBedrock {
		t.Errorf("overlay resolves to %q, want bedrock", got)
	}
}

func TestBuildBackendOverlayAnthropicAWS(t *testing.T) {
	t.Parallel()
	settings, err := BuildBackendOverlay(BackendOverlay{Mode: BackendAnthropicAWS, WorkspaceID: "ws-test"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	env := overlayEnv(t, settings)
	if env["CLAUDE_CODE_USE_ANTHROPIC_AWS"] != "1" {
		t.Errorf("anthropic-aws selector = %q, want 1", env["CLAUDE_CODE_USE_ANTHROPIC_AWS"])
	}
	if env["CLAUDE_CODE_USE_BEDROCK"] != "0" {
		t.Errorf("bedrock competitor = %q, want 0 (bedrock outranks platform-on-aws)", env["CLAUDE_CODE_USE_BEDROCK"])
	}
	if env["ANTHROPIC_AWS_WORKSPACE_ID"] != "ws-test" {
		t.Errorf("workspace id = %q, want ws-test", env["ANTHROPIC_AWS_WORKSPACE_ID"])
	}
	if got := ResolveBackend(lookupFrom(env)); got != BackendAnthropicAWS {
		t.Errorf("overlay resolves to %q, want anthropic-aws", got)
	}
}

func TestBuildBackendOverlayAnthropicAWSRequiresWorkspaceID(t *testing.T) {
	t.Parallel()
	if _, err := BuildBackendOverlay(BackendOverlay{Mode: BackendAnthropicAWS}); err == nil {
		t.Fatal("expected an error for anthropic-aws with no workspace id")
	}
}

func TestBuildBackendOverlaySubscription(t *testing.T) {
	t.Parallel()
	settings, err := BuildBackendOverlay(BackendOverlay{Mode: BackendSubscription})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	env := overlayEnv(t, settings)
	// Subscription pins every selector OFF and sets no selector: a clean
	// resolve to the operator's Max OAuth, and a leaked selector cannot survive.
	for _, s := range backendCompetitorSelectors {
		if env[s] != "0" {
			t.Errorf("selector %s = %q, want 0 for subscription", s, env[s])
		}
	}
	if got := ResolveBackend(lookupFrom(env)); got != BackendDefaultName {
		t.Errorf("subscription overlay resolves to %q, want default (no selector)", got)
	}
	if !BackendMatches(BackendSubscription, ResolveBackend(lookupFrom(env))) {
		t.Error("subscription intent should match its own clean overlay")
	}
}

func TestBuildBackendOverlayUnknownMode(t *testing.T) {
	t.Parallel()
	if _, err := BuildBackendOverlay(BackendOverlay{Mode: Backend("gemini-local")}); err == nil {
		t.Fatal("expected an error for an unrecognised mode")
	}
}

func TestBuildBackendOverlayHelperPointerAndExtra(t *testing.T) {
	t.Parallel()
	settings, err := BuildBackendOverlay(BackendOverlay{
		Mode:                BackendBedrock,
		AWSCredentialExport: "/path/helpers/bedrock-iam.sh",
		Extra:               map[string]string{"AWS_PROFILE": "profile-under-test"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if settings["awsCredentialExport"] != "/path/helpers/bedrock-iam.sh" {
		t.Errorf("awsCredentialExport = %v, want the helper path", settings["awsCredentialExport"])
	}
	env := overlayEnv(t, settings)
	if env["AWS_PROFILE"] != "profile-under-test" {
		t.Errorf("extra AWS_PROFILE = %q, want passthrough value", env["AWS_PROFILE"])
	}
	// No apiKeyHelper key when none was supplied.
	if _, ok := settings["apiKeyHelper"]; ok {
		t.Error("apiKeyHelper present but none was supplied")
	}
}

// The overlay neutralises the value-carrying redirects as well as the boolean
// selectors. Pinning only the booleans inverted the protection relative to
// risk: ResolveBackend returns on the first truthy selector and never reaches
// the value-var loop, so a selector-bearing mode could carry an ambient proxy
// URL into the pane and still verify clean.
func TestBuildBackendOverlayPinsTheValueRedirects(t *testing.T) {
	t.Parallel()
	settings, err := BuildBackendOverlay(BackendOverlay{Mode: BackendBedrock})
	if err != nil {
		t.Fatalf("BuildBackendOverlay: %v", err)
	}
	env := overlayEnv(t, settings)
	for _, name := range backendValueVars {
		got, present := env[name]
		if !present {
			t.Errorf("%s not pinned by the overlay", name)
			continue
		}
		if got != "" {
			t.Errorf("%s = %q, want it pinned empty", name, got)
		}
	}
	// With every redirect neutralised, an ambient proxy URL cannot survive
	// into the effective environment the harness reads.
	if got := ClassifySettingsBackendForTest(env); got != BackendBedrock {
		t.Errorf("overlay classifies as %q, want bedrock", got)
	}
}

// A mode that legitimately needs one of the value vars sets it through Extra,
// which is merged after the pin block and wins.
func TestBuildBackendOverlayLetsAModeSupplyAValueVar(t *testing.T) {
	t.Parallel()
	settings, err := BuildBackendOverlay(BackendOverlay{
		Mode:  BackendBedrock,
		Extra: map[string]string{"ANTHROPIC_BEDROCK_SERVICE_TIER": "priority"},
	})
	if err != nil {
		t.Fatalf("BuildBackendOverlay: %v", err)
	}
	if got := overlayEnv(t, settings)["ANTHROPIC_BEDROCK_SERVICE_TIER"]; got != "priority" {
		t.Errorf("service tier = %q, want the declared value to win", got)
	}
}

// The builder owns the selector block, so a declared selector is ignored
// rather than allowed to override the pins. The guarantee has to hold in the
// builder itself, not in whichever caller happens to pre-filter.
func TestBuildBackendOverlayIgnoresADeclaredSelector(t *testing.T) {
	t.Parallel()
	settings, err := BuildBackendOverlay(BackendOverlay{
		Mode:  BackendSubscription,
		Extra: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"},
	})
	if err != nil {
		t.Fatalf("BuildBackendOverlay: %v", err)
	}
	if got := overlayEnv(t, settings)["CLAUDE_CODE_USE_BEDROCK"]; got != "0" {
		t.Errorf("CLAUDE_CODE_USE_BEDROCK = %q, want the builder's pin to hold", got)
	}
}

// A role that declares a literal bearer is asking marvel to write a third
// party's credential into a file marvel owns. That is custody, not brokering,
// so the build refuses rather than writing it (ADR-009). The design names
// AWS_BEARER_TOKEN_BEDROCK as the static Bedrock bearer.
func TestBuildBackendOverlayRefusesAnInlinedBearer(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "AWS_BEARER_TOKEN_BEDROCK", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			_, err := BuildBackendOverlay(BackendOverlay{
				Mode:  BackendBedrock,
				Extra: map[string]string{key: "a-literal-secret-value"},
			})
			if err == nil {
				t.Fatalf("expected a refusal for an inlined %s", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q does not name the offending key", err)
			}
			if strings.Contains(err.Error(), "a-literal-secret-value") {
				t.Errorf("error leaks the credential value: %q", err)
			}
		})
	}
}

// The design wires awsAuthRefresh for both IAM modes (R6), so the builder has
// to emit it as a settings pointer beside awsCredentialExport.
func TestBuildBackendOverlayEmitsAuthRefreshPointer(t *testing.T) {
	t.Parallel()
	settings, err := BuildBackendOverlay(BackendOverlay{
		Mode:                BackendBedrock,
		AWSCredentialExport: "/opt/creds.sh",
		AWSAuthRefresh:      "/opt/refresh.sh",
	})
	if err != nil {
		t.Fatalf("BuildBackendOverlay: %v", err)
	}
	if got := settings["awsAuthRefresh"]; got != "/opt/refresh.sh" {
		t.Errorf("awsAuthRefresh = %v, want the declared pointer", got)
	}
	if got := settings["awsCredentialExport"]; got != "/opt/creds.sh" {
		t.Errorf("awsCredentialExport = %v, want the declared pointer", got)
	}
	// An undeclared pointer stays absent rather than empty, so the harness
	// never sees a key pointing at nothing.
	bare, err := BuildBackendOverlay(BackendOverlay{Mode: BackendBedrock})
	if err != nil {
		t.Fatalf("BuildBackendOverlay: %v", err)
	}
	if _, ok := bare["awsAuthRefresh"]; ok {
		t.Error("awsAuthRefresh present when none was declared")
	}
}

// ClassifySettingsBackendForTest resolves a backend from a built env block.
// The full settings-file classifier arrives with the verify command; this
// keeps the assertion above honest without reaching forward to it.
func ClassifySettingsBackendForTest(env map[string]string) Backend {
	return ResolveBackend(func(k string) string { return env[k] })
}
