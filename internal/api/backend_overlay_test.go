package api

import "testing"

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
