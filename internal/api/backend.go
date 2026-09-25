package api

import "strings"

// BackendRedirection is the spawn-time verdict on whether a session reaches
// the model vendor's direct API or has been pointed at some other backend.
// It is the "redirection discriminator" of finding-031: the shipped
// context-window table holds the vendor's DIRECT-API windows, so a table
// value applies only to a session on the default backend. The same model id
// through Bedrock, Vertex, or an arbitrary proxy can carry a different
// window (finding-016 axis 4), and marvel cannot observe which backend
// actually served a request — but it CAN observe, at spawn, whether the
// backend-selecting environment departs from the vendor default.
//
// Three states, and only three are answerable: is this session on the
// vendor default, has something redirected it, or did marvel never observe
// its spawn environment? The last is not a fourth axis; it is the absence of
// an observation, and it is the ZERO VALUE on purpose — an adopted pane, an
// ad-hoc session that never ran the classifier, or a record predating this
// field all read as "cannot tell", which the window resolver treats as
// departure from default (finding-031 §4: the guard "may treat cannot tell
// as departure"). Matching the codebase's pessimistic-provenance default
// (usage.KeyConfidence.Soft, ContextSourceNone): an unobserved backend is
// not vouched for.
type BackendRedirection string

const (
	// BackendUnknown is the zero value: marvel did not classify this
	// session's spawn environment. Rendered "cannot tell". The window
	// resolver treats it as departure from the default backend, because a
	// table value keyed on the vendor's direct-API window cannot be
	// vouched for when the backend was never observed.
	BackendUnknown BackendRedirection = ""
	// BackendDefault means every backend-selecting variable was absent (or
	// falsy) at spawn, so the session reaches the vendor's direct API and
	// the shipped table's direct-API windows apply.
	BackendDefault BackendRedirection = "default"
	// BackendRedirected means at least one backend-selecting variable
	// pointed the session off the vendor default — Bedrock, Vertex, a
	// proxy base URL, and the rest of finding-016 axis 4. A table value
	// keyed on the direct-API window does not apply.
	BackendRedirected BackendRedirection = "redirected"
)

// String renders the verdict for logs and diagnostics, giving the zero
// value a name rather than an empty string.
func (b BackendRedirection) String() string {
	switch b {
	case BackendDefault:
		return "default"
	case BackendRedirected:
		return "redirected"
	default:
		return "cannot-tell"
	}
}

// backendFlagVars are the boolean backend switches Claude Code reads
// (measured in the 2.1.226 binary, finding-016 axis 4). Any one set to a
// truthy value redirects the session off the vendor default. This is a
// third party's fact that silently breaks a window when wrong, so it lives
// in Go where it is diff-reviewable and versioned with the binary — the
// same placement rationale as canonicalPermissionModes (see manifest.go).
// It is DERIVED from backendSelectorNames rather than hand-listed beside it.
// The two were separate copies of one fact and drifted: the hand-written
// selector list was short CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD, so a session
// redirected by that variable classified as "redirected" here and resolved to
// "default" three lines away, and the verify command reported ok on it. One
// slice, derived, is the only thing that keeps the two answers consistent.
var backendFlagVars = backendSelectorEnvNames()

// backendSelectorEnvNames lists the selector variables in precedence order.
func backendSelectorEnvNames() []string {
	out := make([]string, 0, len(backendSelectorNames))
	for _, s := range backendSelectorNames {
		out = append(out, s.env)
	}
	return out
}

// backendBearerEnv are environment variables whose VALUE is a credential: a
// bearer at a third party rather than a setting. They are named here, in one
// place, because two different surfaces have to refuse them and neither can be
// the sole guard: the constructed pane environment (which a harness can
// republish wholesale, finding-020) and the settings overlay marvel writes.
//
// ADR-009 draws the custody line at audience, not format. Any of these is
// authority at a vendor, so marvel holding one in a surface it owns is custody
// rather than brokering. The supported shape is a helper POINTER whose stdout
// is the secret (apiKeyHelper, awsCredentialExport), which marvel can carry
// without ever seeing the value.
//
// ANTHROPIC_AUTH_TOKEN is the bearer Claude Code sends to a router such as
// liteLLM, and CLAUDE_CODE_OAUTH_TOKEN is a subscription's long-lived grant;
// both were missing from the first version of this list.
var backendBearerEnv = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"AWS_BEARER_TOKEN_BEDROCK",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"CLAUDE_CODE_OAUTH_TOKEN",
}

// backendBearerSuffixes catch the bearer a hand list has not named yet: a
// new vendor's key (OPENAI_API_KEY) follows the same naming, so a name ending
// in one of these is refused without waiting for someone to add it. The
// match is anchored at the end, so a pointer to a helper
// (MARVEL_BACKEND_API_KEY_HELPER) is not caught. A false positive fails
// closed and is logged by key, which is the safe direction for custody.
var backendBearerSuffixes = []string{
	"_API_KEY",
	"_AUTH_TOKEN",
	"_OAUTH_TOKEN",
	"_SECRET_ACCESS_KEY",
	"_SESSION_TOKEN",
}

// BackendBearerEnv reports whether an env key carries a literal credential
// that must never be inlined into a constructed environment or an overlay.
// It judges the key, never the value, so a helper pointer declared through
// apiKeyHelper or awsCredentialExport stays the supported path.
func BackendBearerEnv(k string) bool {
	for _, b := range backendBearerEnv {
		if b == k {
			return true
		}
	}
	for _, s := range backendBearerSuffixes {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return strings.Contains(k, "_BEARER_TOKEN")
}

// backendValueVars redirect by carrying any value at all: a custom base URL
// points at a proxy, and a Bedrock service tier presupposes Bedrock. A
// non-empty value is treated as departure — conservatively, since even the
// vendor's own canonical URL written here signals non-default intent, and
// the loud-absence-over-silent-wrong bias (finding-031) prefers a soft
// verdict to a confident wrong window.
var backendValueVars = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_BEDROCK_SERVICE_TIER",
}

// ClassifyBackendRedirection reads the backend-selecting environment through
// lookup and returns whether the session is on the vendor default or has
// been redirected. It NEVER returns BackendUnknown: running the classifier
// always yields an answer, so "cannot tell" is reserved for the sessions
// this function was never called on (the zero value).
//
// lookup resolves an environment variable name to its value ("" when
// absent). Taking a lookup rather than calling os.Getenv keeps this pure
// and lets the caller overlay the process environment with any per-session
// overrides it constructed for the pane.
func ClassifyBackendRedirection(lookup func(string) string) BackendRedirection {
	for _, name := range backendFlagVars {
		if backendFlagTruthy(lookup(name)) {
			return BackendRedirected
		}
	}
	for _, name := range backendValueVars {
		if strings.TrimSpace(lookup(name)) != "" {
			return BackendRedirected
		}
	}
	return BackendDefault
}

// backendFlagTruthy reports whether a Claude Code boolean backend switch is
// enabled. Unset, blank, and the explicit falsy spellings are off;
// everything else (the usual "1"/"true", but also any other non-empty
// value an operator might use) is on.
func backendFlagTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// Backend names a specific model backend, richer than the coarse
// BackendRedirection verdict the window resolver consumes. It carries two
// roles in the backend-override design (design-backend-swaps.md, R1/R4/R5):
// the per-role INTENDED backend an operator declares (Runtime.Backend), and
// the NAMED backend the constructed spawn environment resolves to
// (ResolveBackend). marvel records both at spawn; the loud-failure gate that
// WILL compare them and refuse a mismatched launch is BT5 (aae-orc-29f04) and
// is not built yet. Until it lands, a mismatch is reported only when an
// operator runs `marvel backend verify`, so this pair is evidence rather than
// enforcement. Empty means unspecified: no declared intent, or a session the
// classifier never ran on.
type Backend string

const (
	// BackendDefaultName is the vendor direct API with no selector set. It
	// is also where a subscription (Max OAuth) session resolves, because
	// the environment cannot show subscription apart from default; the
	// intent label carries that distinction (BackendSubscription).
	BackendDefaultName Backend = "default"
	// BackendSubscription is an operator's own Max/Pro OAuth, selectors
	// positively OFF (design R7, single-user A2). An intent label only:
	// the environment resolves it to BackendDefaultName.
	BackendSubscription Backend = "subscription"
	BackendBedrock      Backend = "bedrock"
	BackendAnthropicAWS Backend = "anthropic-aws"
	BackendVertex       Backend = "vertex"
	BackendFoundry      Backend = "foundry"
	BackendMantle       Backend = "mantle"
	BackendGateway      Backend = "gateway"
	// BackendAnthropicGoogleCloud is Claude on Google Cloud, the GCP sibling
	// of BackendAnthropicAWS. It exists because the redirect list has always
	// named its selector; without a constant here ResolveBackend had no name
	// to return and fell through to BackendDefaultName.
	BackendAnthropicGoogleCloud Backend = "anthropic-google-cloud"
	// BackendCustom is a base-URL redirect that names no standard selector
	// (a proxy, or a future local backend). Recognised so the intent check
	// is honest; the named local modes are BT13, out of pass 1.
	BackendCustom Backend = "custom"
)

// backendSelectorNames maps each Claude Code boolean selector to the backend
// it names, in the precedence order Claude Code applies. Bedrock and Foundry
// outrank Platform-on-AWS, so turning Platform-on-AWS ON is not enough while
// a Bedrock selector is also ON (research Q, the reason the overlay pins
// competitors OFF rather than only turning one ON). ResolveBackend returns
// the first truthy selector in this order.
var backendSelectorNames = []struct {
	env     string
	backend Backend
}{
	{"CLAUDE_CODE_USE_BEDROCK", BackendBedrock},
	{"CLAUDE_CODE_USE_FOUNDRY", BackendFoundry},
	{"CLAUDE_CODE_USE_VERTEX", BackendVertex},
	{"CLAUDE_CODE_USE_MANTLE", BackendMantle},
	{"CLAUDE_CODE_USE_GATEWAY", BackendGateway},
	{"CLAUDE_CODE_USE_ANTHROPIC_AWS", BackendAnthropicAWS},
	// Placed beside its AWS sibling rather than higher: the research names
	// only the Bedrock and Foundry precedence over Platform-on-AWS, and the
	// vendor docs are silent on the rest, so this claims no rank it cannot
	// support. The overlay pins every competitor OFF precisely so the
	// deterministic attach never depends on this order being right.
	{"CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD", BackendAnthropicGoogleCloud},
}

// ResolveBackend names the backend the constructed spawn environment selects.
// It reads the same variables ClassifyBackendRedirection checks but returns
// the SPECIFIC backend rather than the coarse redirected/default verdict, so
// marvel can compare it against the declared intent. A truthy selector wins
// in precedence order; a base-URL redirect with no selector is BackendCustom;
// nothing set is BackendDefaultName. Like ClassifyBackendRedirection it takes
// a lookup so the caller can overlay the process environment with the
// per-session env it constructed, and it never returns the empty Backend:
// running the classifier always yields a name.
func ResolveBackend(lookup func(string) string) Backend {
	for _, s := range backendSelectorNames {
		if backendFlagTruthy(lookup(s.env)) {
			return s.backend
		}
	}
	for _, name := range backendValueVars {
		if strings.TrimSpace(lookup(name)) != "" {
			return BackendCustom
		}
	}
	return BackendDefaultName
}

// selectorFamily reduces a named backend to what the environment can show:
// subscription is indistinguishable from default in the env (both set no
// selector), so both collapse to BackendDefaultName. Every other backend is
// its own family. This is what BackendMatches compares, so a subscription
// intent agrees with a clean (no-selector) environment while a leaked
// selector does not.
func (b Backend) selectorFamily() Backend {
	if b == BackendSubscription {
		return BackendDefaultName
	}
	return b
}

// BackendMatches reports whether a resolved backend is consistent with the
// declared intent. An empty intent matches anything (no declaration to
// contradict). Otherwise the two must share a selector family, so
// subscription intent matches a clean environment (resolved default) and a
// leaked selector (resolved bedrock) does not. This is the intent-versus-
// actual test the loud-failure gate applies (design R4).
func BackendMatches(intended, resolved Backend) bool {
	if intended == "" {
		return true
	}
	return intended.selectorFamily() == resolved.selectorFamily()
}
