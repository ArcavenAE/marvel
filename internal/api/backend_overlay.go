package api

import "fmt"

// This is the Layer A overlay builder (design-backend-swaps.md): the per-session
// Claude Code settings fragment marvel writes and passes as `claude --settings`.
// A plain env value is a silent no-op against a settings.json that already
// selects a backend, so the reliable attach positively turns the chosen backend
// ON and every competitor OFF. The builder is pure: it takes the mode and the
// non-secret mode inputs a role declared, and returns the settings map. The
// caller (the session manager) writes it 0600 and sweeps it with the session.
// Secrets are never inlined: a credential source is a HELPER POINTER, and its
// stdout is the secret (custody stays out of marvel state, ADR-009).

// backendCompetitorSelectors is every Claude Code boolean backend selector the
// overlay must be able to pin OFF. It is a superset of the modes marvel builds
// today, on purpose: an ambient selector this list omits would survive into the
// session and silently outrank the chosen one, the exact failure the overlay
// exists to prevent. Kept in sync with backendFlagVars.
var backendCompetitorSelectors = []string{
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY",
	"CLAUDE_CODE_USE_MANTLE",
	"CLAUDE_CODE_USE_GATEWAY",
	"CLAUDE_CODE_USE_ANTHROPIC_AWS",
	"CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD",
}

// backendSelectorFor names the single selector a mode turns ON, or "" for the
// selector-less modes (subscription, default), which are distinguished by intent
// rather than by a selector.
func backendSelectorFor(mode Backend) (string, bool) {
	switch mode {
	case BackendBedrock:
		return "CLAUDE_CODE_USE_BEDROCK", true
	case BackendAnthropicAWS:
		return "CLAUDE_CODE_USE_ANTHROPIC_AWS", true
	case BackendVertex:
		return "CLAUDE_CODE_USE_VERTEX", true
	case BackendFoundry:
		return "CLAUDE_CODE_USE_FOUNDRY", true
	case BackendMantle:
		return "CLAUDE_CODE_USE_MANTLE", true
	case BackendGateway:
		return "CLAUDE_CODE_USE_GATEWAY", true
	default:
		return "", false
	}
}

// BackendBuilderOwnedEnv reports whether an env key is one the overlay builder
// sets itself: a competitor selector or ANTHROPIC_API_KEY. A caller merging
// operator-declared passthrough into an overlay skips these so the builder's
// deterministic selector block stays authoritative.
func BackendBuilderOwnedEnv(k string) bool {
	if k == "ANTHROPIC_API_KEY" {
		return true
	}
	for _, s := range backendCompetitorSelectors {
		if s == k {
			return true
		}
	}
	return false
}

// BackendOverlay is the non-secret input to the overlay builder. Everything
// here is safe to hold in marvel state: a workspace id, an AWS profile NAME
// (never the credential), and pointers to helper scripts whose stdout is the
// secret. The literal bearer never appears; it lives behind APIKeyHelper /
// AWSCredentialExport.
type BackendOverlay struct {
	// Mode is the intended backend: bedrock, anthropic-aws, subscription, or
	// default. The IAM variants share a Mode with their static twins and are
	// distinguished by their credential source (AWSCredentialExport / an
	// AWS_PROFILE in Extra), not by the selector.
	Mode Backend
	// WorkspaceID is required for anthropic-aws (sent as anthropic-workspace-id).
	WorkspaceID string
	// APIKeyHelper points at a script whose stdout is the API key, re-run on
	// the harness's own schedule. A brokering pointer, not custody.
	APIKeyHelper string
	// AWSCredentialExport points at a script that prints JSON AWS credentials,
	// the non-interactive refresh path for the IAM modes (BT8). A pointer.
	AWSCredentialExport string
	// Extra is additional non-secret env the role declared to carry into the
	// overlay (for example AWS_PROFILE, ANTHROPIC_AWS_WORKSPACE_ID set directly,
	// or an OTEL attribute). Merged after the selector block, so an operator can
	// add a key the builder does not know; it must not carry a literal secret.
	Extra map[string]string
}

// BuildBackendOverlay returns the Claude Code settings map for a mode: the
// chosen selector ON, every competitor pinned OFF, ANTHROPIC_API_KEY pinned so
// an ambient key cannot win, the mode's required fields, and any helper pointer.
// It refuses a mode it does not recognise and refuses anthropic-aws without a
// workspace id (the required field, the shakedown's mode-2 red), so a
// misconfigured overlay fails at build rather than launching wrong.
func BuildBackendOverlay(o BackendOverlay) (map[string]any, error) {
	env := map[string]string{}
	// Pin every competitor OFF first; the chosen selector is turned back ON
	// below. Bedrock and Foundry outrank Platform-on-AWS, so ON alone is not
	// enough (research); pinning the whole set OFF is what makes the attach
	// deterministic regardless of the ambient environment.
	for _, s := range backendCompetitorSelectors {
		env[s] = "0"
	}
	// An ambient ANTHROPIC_API_KEY outranks a selector, so pin it empty for
	// every managed mode; a mode that needs a key supplies it through a helper
	// pointer, not this variable.
	env["ANTHROPIC_API_KEY"] = ""

	switch o.Mode {
	case BackendSubscription, BackendDefaultName, "":
		// Selector-less: every CLAUDE_CODE_USE_* stays pinned OFF, no key, auth
		// resolves to the operator's own Max OAuth (subscription) or the ambient
		// chain (default). Default normally writes no overlay at all; when one is
		// requested for it, this is the shape.
	default:
		selector, ok := backendSelectorFor(o.Mode)
		if !ok {
			return nil, fmt.Errorf("build backend overlay: unrecognised mode %q", o.Mode)
		}
		env[selector] = "1"
		if o.Mode == BackendAnthropicAWS {
			if o.WorkspaceID == "" {
				return nil, fmt.Errorf("build backend overlay: mode %q requires a workspace id (ANTHROPIC_AWS_WORKSPACE_ID)", o.Mode)
			}
			env["ANTHROPIC_AWS_WORKSPACE_ID"] = o.WorkspaceID
		}
	}

	// Operator-declared passthrough wins over the builder's defaults, so a role
	// can add a key the builder does not model (AWS_PROFILE for an IAM mode, an
	// OTEL attribute). It must not carry a literal secret.
	for k, v := range o.Extra {
		env[k] = v
	}

	settings := map[string]any{"env": env}
	if o.APIKeyHelper != "" {
		settings["apiKeyHelper"] = o.APIKeyHelper
	}
	if o.AWSCredentialExport != "" {
		settings["awsCredentialExport"] = o.AWSCredentialExport
	}
	return settings, nil
}
