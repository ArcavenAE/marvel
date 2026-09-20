package api

import "testing"

func TestResolveBackendCredentialSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		overlay BackendOverlay
		want    BackendCredentialSource
	}{
		{"nothing declared is ambient", BackendOverlay{Mode: BackendBedrock}, BackendCredentialAmbient},
		{"export helper", BackendOverlay{Mode: BackendBedrock, AWSCredentialExport: "/opt/creds.sh"}, BackendCredentialAWSExport},
		{"profile name", BackendOverlay{Mode: BackendBedrock, Extra: map[string]string{awsProfileEnv: "eng"}}, BackendCredentialAWSProfile},
		{"api key helper", BackendOverlay{Mode: BackendAnthropicAWS, APIKeyHelper: "/opt/key.sh"}, BackendCredentialAPIKeyHelper},
		{"auth refresh hook", BackendOverlay{Mode: BackendBedrock, AWSAuthRefresh: "/opt/refresh.sh"}, BackendCredentialAWSAuthRefresh},
		{"export outranks profile", BackendOverlay{Mode: BackendBedrock, AWSCredentialExport: "/opt/creds.sh", Extra: map[string]string{awsProfileEnv: "eng"}}, BackendCredentialAWSExport},
		{"export outranks auth refresh", BackendOverlay{Mode: BackendBedrock, AWSCredentialExport: "/opt/creds.sh", AWSAuthRefresh: "/opt/refresh.sh"}, BackendCredentialAWSExport},
		{"auth refresh outranks a bare profile", BackendOverlay{Mode: BackendBedrock, AWSAuthRefresh: "/opt/refresh.sh", Extra: map[string]string{awsProfileEnv: "eng"}}, BackendCredentialAWSAuthRefresh},
		{"profile outranks api key helper", BackendOverlay{Mode: BackendBedrock, APIKeyHelper: "/opt/key.sh", Extra: map[string]string{awsProfileEnv: "eng"}}, BackendCredentialAWSProfile},
		{"blank declarations are not a source", BackendOverlay{Mode: BackendBedrock, AWSCredentialExport: "   ", Extra: map[string]string{awsProfileEnv: " "}}, BackendCredentialAmbient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveBackendCredentialSource(tt.overlay); got != tt.want {
				t.Errorf("ResolveBackendCredentialSource() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A profile names an identity but not how it refreshes, so it is deliberately
// not vouched for: the same spelling covers credential_process and an SSO
// profile that wants a browser, and marvel does not read the AWS config.
func TestBackendCredentialSourceRefreshesNonInteractively(t *testing.T) {
	t.Parallel()
	tests := []struct {
		src  BackendCredentialSource
		want bool
	}{
		{BackendCredentialAWSExport, true},
		{BackendCredentialAPIKeyHelper, true},
		{BackendCredentialAWSProfile, false},
		{BackendCredentialAWSAuthRefresh, false},
		{BackendCredentialAmbient, false},
		{BackendCredentialUnknown, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.src), func(t *testing.T) {
			t.Parallel()
			if got := tt.src.RefreshesNonInteractively(); got != tt.want {
				t.Errorf("%q.RefreshesNonInteractively() = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

func TestBackendIsIAMSessionAndRefreshVouched(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		backend     Backend
		src         BackendCredentialSource
		wantIAM     bool
		wantVouched bool
	}{
		{"bedrock with export helper", BackendBedrock, BackendCredentialAWSExport, true, true},
		{"bedrock with profile only", BackendBedrock, BackendCredentialAWSProfile, true, false},
		{"bedrock with only an auth refresh hook", BackendBedrock, BackendCredentialAWSAuthRefresh, true, false},
		{"bedrock with static keys", BackendBedrock, BackendCredentialAmbient, false, true},
		{"platform-on-aws with export helper", BackendAnthropicAWS, BackendCredentialAWSExport, true, true},
		{"platform-on-aws with profile only", BackendAnthropicAWS, BackendCredentialAWSProfile, true, false},
		{"vertex is not an aws identity", BackendVertex, BackendCredentialAWSProfile, false, true},
		{"subscription has nothing to refresh", BackendSubscription, BackendCredentialAmbient, false, true},
		{"default has nothing to refresh", BackendDefaultName, BackendCredentialAmbient, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := BackendIsIAMSession(tt.backend, tt.src); got != tt.wantIAM {
				t.Errorf("BackendIsIAMSession(%q, %q) = %v, want %v", tt.backend, tt.src, got, tt.wantIAM)
			}
			if got := BackendRefreshVouched(tt.backend, tt.src); got != tt.wantVouched {
				t.Errorf("BackendRefreshVouched(%q, %q) = %v, want %v", tt.backend, tt.src, got, tt.wantVouched)
			}
		})
	}
}

func TestBackendVariantLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		backend Backend
		src     BackendCredentialSource
		want    string
	}{
		{BackendBedrock, BackendCredentialAWSExport, "bedrock (IAM session)"},
		{BackendBedrock, BackendCredentialAWSProfile, "bedrock (IAM session)"},
		{BackendBedrock, BackendCredentialAWSAuthRefresh, "bedrock (IAM session)"},
		{BackendBedrock, BackendCredentialAmbient, "bedrock (static key)"},
		{BackendAnthropicAWS, BackendCredentialAWSExport, "anthropic-aws (IAM session)"},
		{BackendAnthropicAWS, BackendCredentialAmbient, "anthropic-aws (static key)"},
		{BackendVertex, BackendCredentialAmbient, "vertex"},
		{BackendSubscription, BackendCredentialAmbient, "subscription"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := BackendVariantLabel(tt.backend, tt.src); got != tt.want {
				t.Errorf("BackendVariantLabel(%q, %q) = %q, want %q", tt.backend, tt.src, got, tt.want)
			}
		})
	}
}
