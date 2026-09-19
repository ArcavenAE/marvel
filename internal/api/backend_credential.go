package api

import "strings"

// BackendCredentialSource names HOW a session's backend obtains its
// credentials. The backend-selecting environment cannot show this: a Bedrock
// session driven by a short-lived IAM session and one driven by static keys
// set the same selector, so ResolveBackend returns the same name for both
// (design Q3). The distinction matters anyway, because only one of the two
// expires mid-shift, so it is carried separately, named from what the role
// DECLARED rather than from anything marvel reads out of a credential store.
//
// Every value here is a non-secret fact about configuration: a helper script
// path, a profile NAME, or the absence of either. No bearer token is named,
// held, or logged (ADR-009); the helper's stdout is the secret and it stays
// between the harness and the helper.
type BackendCredentialSource string

const (
	// BackendCredentialUnknown is the zero value: marvel never classified
	// this session's credential source. Like BackendUnknown it is the
	// absence of an observation rather than a fourth kind of source, and it
	// is what a record predating this field reads as.
	BackendCredentialUnknown BackendCredentialSource = ""
	// BackendCredentialAWSExport is an awsCredentialExport helper pointer:
	// a script that prints JSON AWS credentials on demand. This is the
	// non-interactive refresh path the IAM modes need to survive hour two
	// without a human (BT8), because the harness can re-run it itself.
	BackendCredentialAWSExport BackendCredentialSource = "aws-credential-export"
	// BackendCredentialAWSProfile is a declared AWS_PROFILE name. It names
	// an AWS identity but NOT how that identity refreshes: the same profile
	// spelling covers a credential_process (non-interactive) and an SSO
	// profile that wants a browser. Marvel does not read the AWS config to
	// find out, so this source is deliberately not vouched for.
	BackendCredentialAWSProfile BackendCredentialSource = "aws-profile"
	// BackendCredentialAPIKeyHelper is an apiKeyHelper pointer: a script
	// whose stdout is the API key, re-run on the harness's own schedule.
	BackendCredentialAPIKeyHelper BackendCredentialSource = "api-key-helper"
	// BackendCredentialAmbient means the role declared no source at all, so
	// the session authenticates with whatever the pane inherits. Honest for
	// the static-key twins and for subscription, where the operator's own
	// login is the point.
	BackendCredentialAmbient BackendCredentialSource = "ambient"
)

// awsProfileEnv is the declared profile NAME (never a credential) that marks
// an AWS identity in a role's passthrough env.
const awsProfileEnv = "AWS_PROFILE"

// ResolveBackendCredentialSource names the credential source a role declared,
// in the order the harness would reach for one: an explicit AWS export helper
// outranks a profile name, which outranks an API-key helper, and a role that
// declared none is ambient. Like ResolveBackend it never returns the zero
// value, so BackendCredentialUnknown stays reserved for the sessions this was
// never run on.
func ResolveBackendCredentialSource(o BackendOverlay) BackendCredentialSource {
	switch {
	case strings.TrimSpace(o.AWSCredentialExport) != "":
		return BackendCredentialAWSExport
	case strings.TrimSpace(o.Extra[awsProfileEnv]) != "":
		return BackendCredentialAWSProfile
	case strings.TrimSpace(o.APIKeyHelper) != "":
		return BackendCredentialAPIKeyHelper
	default:
		return BackendCredentialAmbient
	}
}

// RefreshesNonInteractively reports whether the harness can renew this source
// by itself when a credential expires. Only the two helper pointers qualify:
// the harness re-runs the script and gets fresh output. A profile name does
// not, on purpose (see BackendCredentialAWSProfile) - marvel will not claim a
// refresh it cannot see, and an unvouched source is reported rather than
// assumed good.
func (s BackendCredentialSource) RefreshesNonInteractively() bool {
	switch s {
	case BackendCredentialAWSExport, BackendCredentialAPIKeyHelper:
		return true
	default:
		return false
	}
}

// backendUsesAWSIdentity reports whether a backend authenticates through an
// AWS identity, which is what makes the IAM-versus-static twins possible.
func backendUsesAWSIdentity(b Backend) bool {
	return b == BackendBedrock || b == BackendAnthropicAWS
}

// BackendIsIAMSession reports whether a mode plus its declared source is one
// of the two IAM-session modes: an AWS backend whose identity comes from a
// session that expires (an export helper or a profile), rather than from
// static keys that do not.
func BackendIsIAMSession(b Backend, src BackendCredentialSource) bool {
	if !backendUsesAWSIdentity(b) {
		return false
	}
	switch src {
	case BackendCredentialAWSExport, BackendCredentialAWSProfile:
		return true
	default:
		return false
	}
}

// BackendRefreshVouched reports whether a mode will survive a credential
// expiry without a human. Only the IAM-session modes can fail this: every
// other mode either holds a credential that does not expire or is the
// operator's own login, so there is nothing for marvel to vouch for. This is
// the BT8 question, and the loud-failure gate (BT5) asks it at spawn.
func BackendRefreshVouched(b Backend, src BackendCredentialSource) bool {
	if !BackendIsIAMSession(b, src) {
		return true
	}
	return src.RefreshesNonInteractively()
}

// BackendVariantLabel renders a backend for an operator, naming the IAM or
// static variant for the AWS backends whose selector cannot distinguish them.
// Non-AWS backends render as their plain name, because they have no twin.
func BackendVariantLabel(b Backend, src BackendCredentialSource) string {
	if !backendUsesAWSIdentity(b) {
		return string(b)
	}
	if BackendIsIAMSession(b, src) {
		return string(b) + " (iam)"
	}
	return string(b) + " (static)"
}
