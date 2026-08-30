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

// backendFlagVars are the boolean backend switches Claude Code reads
// (measured in the 2.1.226 binary, finding-016 axis 4). Any one set to a
// truthy value redirects the session off the vendor default. This is a
// third party's fact that silently breaks a window when wrong, so it lives
// in Go where it is diff-reviewable and versioned with the binary — the
// same placement rationale as canonicalPermissionModes (see manifest.go).
var backendFlagVars = []string{
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY",
	"CLAUDE_CODE_USE_MANTLE",
	"CLAUDE_CODE_USE_GATEWAY",
	"CLAUDE_CODE_USE_ANTHROPIC_AWS",
	"CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD",
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
