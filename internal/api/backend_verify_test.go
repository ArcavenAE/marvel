package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClassifySettingsBackend(t *testing.T) {
	t.Parallel()
	built, err := BuildBackendOverlay(BackendOverlay{Mode: BackendBedrock})
	if err != nil {
		t.Fatalf("BuildBackendOverlay: %v", err)
	}
	// The same overlay after a round trip through disk, where encoding/json
	// turns the env block into map[string]any. Both shapes must classify the
	// same, or verification passes in process and fails against the file.
	raw, err := json.Marshal(built)
	if err != nil {
		t.Fatalf("marshal overlay: %v", err)
	}
	var roundTripped map[string]any
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("unmarshal overlay: %v", err)
	}

	tests := []struct {
		name     string
		settings map[string]any
		want     Backend
	}{
		{"built in process", built, BackendBedrock},
		{"read back from disk", roundTripped, BackendBedrock},
		{"no env block", map[string]any{"apiKeyHelper": "/opt/key.sh"}, BackendDefaultName},
		{"selectors pinned off", map[string]any{"env": map[string]string{"CLAUDE_CODE_USE_BEDROCK": "0"}}, BackendDefaultName},
		{"base url with no selector", map[string]any{"env": map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.internal"}}, BackendCustom},
		{"non-string env values are skipped", map[string]any{"env": map[string]any{"CLAUDE_CODE_USE_VERTEX": true}}, BackendDefaultName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifySettingsBackend(tt.settings); got != tt.want {
				t.Errorf("ClassifySettingsBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Bedrock and Foundry outrank Platform-on-AWS, which is why the builder pins
// every competitor OFF rather than only turning the chosen one ON. Verifying
// against the file has to see that too.
func TestClassifySettingsBackendHonoursPrecedence(t *testing.T) {
	t.Parallel()
	settings := map[string]any{"env": map[string]string{
		"CLAUDE_CODE_USE_BEDROCK":       "1",
		"CLAUDE_CODE_USE_ANTHROPIC_AWS": "1",
	}}
	if got := ClassifySettingsBackend(settings); got != BackendBedrock {
		t.Errorf("ClassifySettingsBackend() = %q, want %q", got, BackendBedrock)
	}
}

func overlayFor(t *testing.T, o BackendOverlay) map[string]any {
	t.Helper()
	settings, err := BuildBackendOverlay(o)
	if err != nil {
		t.Fatalf("BuildBackendOverlay(%q): %v", o.Mode, err)
	}
	return settings
}

func TestVerifyBackend(t *testing.T) {
	t.Parallel()
	bedrockOverlay := overlayFor(t, BackendOverlay{Mode: BackendBedrock})
	vertexOverlay := overlayFor(t, BackendOverlay{Mode: BackendVertex})

	tests := []struct {
		name        string
		in          BackendVerifyInput
		wantOK      bool
		wantLabel   string
		wantProblem string
	}{
		{
			name: "healthy iam bedrock session",
			in: BackendVerifyInput{
				Session: "aae/dev/1", Intended: BackendBedrock, Resolved: BackendBedrock,
				CredentialSource: BackendCredentialAWSExport,
				OverlayPath:      "/tmp/ov.json", OverlaySettings: bedrockOverlay,
			},
			wantOK: true, wantLabel: "bedrock (IAM session)",
		},
		{
			name: "subscription needs an overlay and agrees with a clean environment",
			in: BackendVerifyInput{
				Session: "aae/dev/2", Intended: BackendSubscription, Resolved: BackendDefaultName,
				CredentialSource: BackendCredentialAmbient,
				OverlayPath:      "/tmp/ov.json", OverlaySettings: overlayFor(t, BackendOverlay{Mode: BackendSubscription}),
			},
			wantOK: true, wantLabel: "default",
		},
		{
			name: "default mode warrants no overlay",
			in: BackendVerifyInput{
				Session: "aae/dev/3", Intended: BackendDefaultName, Resolved: BackendDefaultName,
				CredentialSource: BackendCredentialAmbient,
			},
			wantOK: true, wantLabel: "default",
		},
		{
			name: "declared bedrock but resolved default is the silent redirect",
			in: BackendVerifyInput{
				Session: "aae/dev/4", Intended: BackendBedrock, Resolved: BackendDefaultName,
				CredentialSource: BackendCredentialAWSExport,
				OverlayPath:      "/tmp/ov.json", OverlaySettings: bedrockOverlay,
			},
			wantOK: false, wantProblem: "resolves to default",
		},
		{
			name: "overlay missing where one was warranted",
			in: BackendVerifyInput{
				Session: "aae/dev/5", Intended: BackendBedrock, Resolved: BackendBedrock,
				CredentialSource: BackendCredentialAWSExport,
				OverlayPath:      "/tmp/missing.json",
			},
			wantOK: false, wantProblem: "no backend overlay at /tmp/missing.json",
		},
		{
			name: "overlay selects a different backend than declared",
			in: BackendVerifyInput{
				Session: "aae/dev/6", Intended: BackendBedrock, Resolved: BackendBedrock,
				CredentialSource: BackendCredentialAWSExport,
				OverlayPath:      "/tmp/ov.json", OverlaySettings: vertexOverlay,
			},
			wantOK: false, wantProblem: "selects vertex",
		},
		{
			name: "iam session with only a profile is not vouched for",
			in: BackendVerifyInput{
				Session: "aae/dev/7", Intended: BackendBedrock, Resolved: BackendBedrock,
				CredentialSource: BackendCredentialAWSProfile,
				OverlayPath:      "/tmp/ov.json", OverlaySettings: bedrockOverlay,
			},
			wantOK: false, wantLabel: "bedrock (IAM session)", wantProblem: "no refresh marvel can vouch for",
		},
		{
			name: "static bedrock has nothing to refresh",
			in: BackendVerifyInput{
				Session: "aae/dev/8", Intended: BackendBedrock, Resolved: BackendBedrock,
				CredentialSource: BackendCredentialAmbient,
				OverlayPath:      "/tmp/ov.json", OverlaySettings: bedrockOverlay,
			},
			wantOK: true, wantLabel: "bedrock (static key)",
		},
		{
			name: "unclassified session cannot tell",
			in: BackendVerifyInput{
				Session: "aae/dev/9", CredentialSource: BackendCredentialUnknown,
			},
			wantOK: false, wantProblem: "never classified",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := VerifyBackend(tt.in)
			if got.OK != tt.wantOK {
				t.Errorf("OK = %v, want %v (problems: %v)", got.OK, tt.wantOK, got.Problems)
			}
			if tt.wantLabel != "" && got.Label != tt.wantLabel {
				t.Errorf("Label = %q, want %q", got.Label, tt.wantLabel)
			}
			if tt.wantProblem == "" {
				return
			}
			joined := strings.Join(got.Problems, " | ")
			if !strings.Contains(joined, tt.wantProblem) {
				t.Errorf("problems %q do not mention %q", joined, tt.wantProblem)
			}
		})
	}
}

// The overlay is what the harness actually reads, so an overlay that disagrees
// with the spawn-time record is reported even when both are plausible on their
// own. This is the case a stale overlay from a previous launch produces.
func TestVerifyBackendReportsOverlayAgainstRecord(t *testing.T) {
	t.Parallel()
	got := VerifyBackend(BackendVerifyInput{
		Session: "aae/dev/10", Intended: "", Resolved: BackendVertex,
		CredentialSource: BackendCredentialAmbient,
		OverlayPath:      "/tmp/ov.json",
		OverlaySettings:  overlayFor(t, BackendOverlay{Mode: BackendBedrock}),
	})
	if got.OK {
		t.Fatalf("expected a problem, got OK with %+v", got)
	}
	if !strings.Contains(strings.Join(got.Problems, " | "), "marvel recorded vertex at spawn") {
		t.Errorf("problems %v do not name the disagreement", got.Problems)
	}
}

// Each unvouched source has its own limit, and the report names which one. The
// distinction is the useful part: an operator fixes a bare profile differently
// from a refresh hook marvel cannot see inside.
func TestVerifyBackendNamesTheRefreshGap(t *testing.T) {
	t.Parallel()
	overlay := overlayFor(t, BackendOverlay{Mode: BackendBedrock})
	tests := []struct {
		src  BackendCredentialSource
		want string
	}{
		{BackendCredentialAWSProfile, "does not read the AWS config"},
		{BackendCredentialAWSAuthRefresh, "awsAuthRefresh hook but no awsCredentialExport"},
	}
	for _, tt := range tests {
		t.Run(string(tt.src), func(t *testing.T) {
			t.Parallel()
			got := VerifyBackend(BackendVerifyInput{
				Session: "aae/dev/x", Intended: BackendBedrock, Resolved: BackendBedrock,
				CredentialSource: tt.src,
				OverlayPath:      "/tmp/ov.json", OverlaySettings: overlay,
			})
			if got.OK {
				t.Fatal("expected an unvouched refresh to be reported")
			}
			joined := strings.Join(got.Problems, " | ")
			if !strings.Contains(joined, tt.want) {
				t.Errorf("problems %q do not name the gap %q", joined, tt.want)
			}
		})
	}
}
