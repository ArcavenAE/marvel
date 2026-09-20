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
// overlay must be able to pin OFF: an ambient selector this set omits would
// survive into the session and silently outrank the chosen one, the exact
// failure the overlay exists to prevent.
//
// It IS backendFlagVars, not a copy of it. A third hand-maintained list here,
// synchronised by a comment, is how the first two drifted; the set the
// classifier treats as redirecting and the set the overlay neutralises have to
// be the same set or the overlay cannot make the attach deterministic.
var backendCompetitorSelectors = backendFlagVars

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

// backendSelectorOwned reports whether an env key is part of the selector block
// the builder sets deterministically.
func backendSelectorOwned(k string) bool {
	for _, s := range backendCompetitorSelectors {
		if s == k {
			return true
		}
	}
	return false
}

// BackendBuilderOwnedEnv reports whether an env key is one the overlay builder
// sets itself: a competitor selector or ANTHROPIC_API_KEY.
//
// This is no longer the gate. The builder enforces both classes itself, so a
// caller does not have to pre-filter; it remains exported for callers that want
// to describe the split without duplicating it.
func BackendBuilderOwnedEnv(k string) bool {
	return k == "ANTHROPIC_API_KEY" || backendSelectorOwned(k)
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
	// Pin the value-carrying redirects empty as well. This package treats a
	// non-empty ANTHROPIC_BASE_URL or ANTHROPIC_BEDROCK_SERVICE_TIER as a
	// departure from the default backend, so leaving them ambient means a
	// session an operator believes is pinned to Bedrock carries an untouched
	// proxy URL into the pane. Pinning only the booleans inverted the
	// protection relative to risk: the selector-less modes were covered and
	// the selector-bearing modes the overlay exists for were not, because
	// ResolveBackend returns on the first truthy selector and never reaches
	// the value-var loop. A mode that legitimately needs one of these sets it
	// through Extra, which is merged after this block and wins.
	for _, name := range backendValueVars {
		env[name] = ""
	}

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
	// can add a key the builder does not model (AWS_PROFILE for an IAM mode, a
	// service tier, an OTEL attribute). Two classes are handled here rather
	// than by the caller, because a guarantee enforced in an out-of-package
	// caller is not a guarantee this exported builder can make:
	//
	//   - A literal bearer is REFUSED. ADR-009 draws the custody line at
	//     authority held at a third party, and the overlay is a file marvel
	//     owns and writes, so inlining one puts marvel in custody. The
	//     supported shape is a helper pointer whose stdout is the secret.
	//   - A builder-owned selector is IGNORED, keeping the ruling that the
	//     builder owns the selector block so a declared selector cannot
	//     override the pins. Ignoring rather than refusing is deliberate: the
	//     pins are the point, and a role that names its mode twice is
	//     redundant rather than wrong.
	for k, v := range o.Extra {
		if BackendBearerEnv(k) {
			return nil, fmt.Errorf("build backend overlay: %s carries a literal credential; declare a helper pointer (awsCredentialExport or apiKeyHelper) instead", k)
		}
		if backendSelectorOwned(k) {
			continue
		}
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
