package api

import "fmt"

// The verification surface (design-backend-swaps.md R5, BT7). macOS cannot
// read a sibling process's environ (research A1), so marvel cannot ask a live
// pane what backend it ended up on. What it CAN do is verify against artifacts
// it controls: the settings overlay it wrote and passed as claude --settings,
// and the intent/actual pair it recorded at spawn. That is what this file
// classifies, and it is deliberately pure - the caller supplies the recorded
// facts, so the verdict is testable without a daemon, a pane, or a filesystem.

// BackendOverlayWarranted reports whether a declared backend needs a settings
// overlay. An empty or default backend accepts the ambient environment and
// writes none; every other mode gets one, including subscription, which must
// still pin the competitor selectors OFF.
func BackendOverlayWarranted(b Backend) bool {
	switch b {
	case "", BackendDefaultName:
		return false
	default:
		return true
	}
}

// backendSettingsEnv reads the env block out of a Claude Code settings map. It
// accepts both shapes the block legitimately takes: map[string]string as the
// builder produces it in process, and map[string]any as encoding/json hands it
// back when the file is read from disk. Anything else yields no env, which the
// caller reports rather than guessing past.
func backendSettingsEnv(settings map[string]any) map[string]string {
	switch env := settings["env"].(type) {
	case map[string]string:
		return env
	case map[string]any:
		out := make(map[string]string, len(env))
		for k, v := range env {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
		return out
	default:
		return nil
	}
}

// ClassifySettingsBackend names the backend a Claude Code settings file
// selects, the artifact-side counterpart to ResolveBackend over an
// environment. This is the detect_backend step of the verification command:
// classify the file marvel wrote, because the live process environment is not
// readable from outside the process.
func ClassifySettingsBackend(settings map[string]any) Backend {
	env := backendSettingsEnv(settings)
	return ResolveBackend(func(k string) string { return env[k] })
}

// BackendVerifyInput is everything the verdict is computed from: what the
// operator declared, what marvel recorded at spawn, and what the overlay on
// disk actually says. The caller gathers these; nothing here reaches out.
type BackendVerifyInput struct {
	// Session names the session being verified, for the rendered report.
	Session string
	// Intended and Resolved are the pair recorded at spawn
	// (Session.BackendIntended / Session.BackendResolved). Both empty means
	// marvel never classified this session.
	Intended Backend
	Resolved Backend
	// CredentialSource is the declared source for this session's role, which
	// names the IAM-versus-static variant the selector cannot show.
	CredentialSource BackendCredentialSource
	// OverlayPath is where the per-session overlay belongs, and
	// OverlaySettings is what was read from it. Nil settings with a
	// non-empty path means the file was expected and is not there.
	OverlayPath     string
	OverlaySettings map[string]any
}

// BackendVerification is the verdict the verify command prints: the named
// backend at each layer, and every disagreement between them. OK is the
// healthy case - intent, spawn-time classification, and the overlay on disk
// all agree, and a session that expires can renew itself.
type BackendVerification struct {
	Session string
	// Intended, Resolved, and Overlay are the three layers, named. Overlay
	// is empty when no overlay was warranted or none was found.
	Intended Backend
	Resolved Backend
	Overlay  Backend
	// CredentialSource and Label carry the variant the selector cannot show:
	// Label is Resolved rendered for an operator, "bedrock (iam)" and such.
	CredentialSource BackendCredentialSource
	Label            string
	// OverlayPath is the file the verdict was read from, blank when none was
	// warranted. OverlayPresent distinguishes "not warranted" from "missing".
	OverlayPath    string
	OverlayPresent bool
	// Problems names each disagreement in operator terms. OK is len == 0.
	Problems []string
	OK       bool
}

// VerifyBackend computes the verdict for one session. It reports rather than
// repairs: every check that fails adds a named problem, and the caller decides
// whether that is a warning or an exit code. Four things can disagree - marvel
// never classified the session at all, the environment resolved to a backend
// the role did not declare, the overlay on disk selects something else again,
// or an IAM session has no refresh marvel can vouch for (BT8).
func VerifyBackend(in BackendVerifyInput) BackendVerification {
	v := BackendVerification{
		Session:          in.Session,
		Intended:         in.Intended,
		Resolved:         in.Resolved,
		CredentialSource: in.CredentialSource,
		OverlayPath:      in.OverlayPath,
	}
	v.Label = BackendVariantLabel(in.Resolved, in.CredentialSource)

	// An unclassified session is the "cannot tell" case, not a pass: an
	// adopted pane or a record predating the fields never had its spawn
	// environment observed, and the house bias is loud absence over a
	// confident wrong answer (finding-031).
	if in.Resolved == "" {
		v.Problems = append(v.Problems, "marvel never classified this session's backend (adopted pane, or a record predating the field)")
	} else if !BackendMatches(in.Intended, in.Resolved) {
		v.Problems = append(v.Problems, fmt.Sprintf("declared %s but the spawn environment resolves to %s", in.Intended, in.Resolved))
	}

	// Presence drives inspection, not whether an overlay was warranted: a file
	// found for an undeclared session is a stale artifact from an earlier
	// launch, and it is exactly the thing that would be read if it were still
	// stamped into the pane. The warranted check governs only the case where a
	// file that should exist does not.
	if in.OverlaySettings == nil {
		if BackendOverlayWarranted(in.Intended) {
			v.Problems = append(v.Problems, fmt.Sprintf("no backend overlay at %s, so the harness was launched without the --settings that makes the backend stick", in.OverlayPath))
		}
	} else {
		v.OverlayPresent = true
		v.Overlay = ClassifySettingsBackend(in.OverlaySettings)
		if !BackendMatches(in.Intended, v.Overlay) {
			v.Problems = append(v.Problems, fmt.Sprintf("overlay at %s selects %s, not the declared %s", in.OverlayPath, v.Overlay, in.Intended))
		} else if in.Resolved != "" && v.Overlay.selectorFamily() != in.Resolved.selectorFamily() {
			v.Problems = append(v.Problems, fmt.Sprintf("overlay selects %s but marvel recorded %s at spawn", v.Overlay, in.Resolved))
		}
	}

	if !BackendRefreshVouched(in.Resolved, in.CredentialSource) {
		v.Problems = append(v.Problems, fmt.Sprintf("%s has no refresh marvel can vouch for (declared source %s); an expiry mid-shift needs a human", v.Label, in.CredentialSource))
	}

	v.OK = len(v.Problems) == 0
	return v
}
