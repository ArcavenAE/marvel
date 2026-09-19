package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

func TestRenderBackendVerifications(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		report   []api.BackendVerification
		contains []string
		absent   []string
	}{
		{
			name:     "no sessions",
			report:   nil,
			contains: []string{"No sessions to verify."},
		},
		{
			name: "healthy iam session names its variant",
			report: []api.BackendVerification{{
				Session: "aae/dev-1", Intended: api.BackendBedrock, Resolved: api.BackendBedrock,
				Overlay: api.BackendBedrock, OverlayPresent: true,
				CredentialSource: api.BackendCredentialAWSExport,
				Label:            "bedrock (IAM session)", OK: true,
			}},
			contains: []string{"aae/dev-1", "bedrock (IAM session)", "aws-credential-export", "ok"},
			absent:   []string{"PROBLEM", "MISSING"},
		},
		{
			name: "a warranted overlay that is absent reads as MISSING",
			report: []api.BackendVerification{{
				Session: "aae/dev-2", Intended: api.BackendBedrock, Resolved: api.BackendBedrock,
				Label: "bedrock (static key)", CredentialSource: api.BackendCredentialAmbient,
				Problems: []string{"no backend overlay at /tmp/x.json"}, OK: false,
			}},
			contains: []string{"MISSING", "PROBLEM", "aae/dev-2: no backend overlay at /tmp/x.json"},
		},
		{
			name: "default mode wants no overlay",
			report: []api.BackendVerification{{
				Session: "aae/dev-3", Intended: api.BackendDefaultName, Resolved: api.BackendDefaultName,
				Label: "default", CredentialSource: api.BackendCredentialAmbient, OK: true,
			}},
			contains: []string{"none", "ok"},
			absent:   []string{"MISSING"},
		},
		{
			name: "an unclassified session renders absence as a dash",
			report: []api.BackendVerification{{
				Session:  "aae/dev-4",
				Problems: []string{"marvel never classified this session's backend"}, OK: false,
			}},
			contains: []string{"-", "PROBLEM", "never classified"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := renderBackendVerifications(tt.report)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("render missing %q:\n%s", want, got)
				}
			}
			for _, unwanted := range tt.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("render unexpectedly contains %q:\n%s", unwanted, got)
				}
			}
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("render does not end in a newline:\n%q", got)
			}
		})
	}
}
