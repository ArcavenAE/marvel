package daemon

import (
	"fmt"
	"strings"
)

// Scope is the authority a marvel key carries on this daemon. Keys are scoped
// so a key enrolled on another operator's daemon holds only what a task needs.
// A key with no recorded scope is admin, which preserves the behavior from
// before scopes existed: every authorized key was full daemon admin
// (brief 9 S5, aae-orc-rlb8h).
type Scope string

const (
	// ScopeAdmin may call every daemon method. It is also the scope of the
	// local unix socket caller (the daemon owner) and of any authorized key
	// with no marvel-scope option.
	ScopeAdmin Scope = "admin"

	// ScopeCredentialPush may push, list, and delete credentials and nothing
	// else. It cannot read a credential value and cannot reach any other
	// method, so an operator enrolling on a second party's daemon holds only
	// the reach that bus-credential enrollment needs, not logs, inject, or
	// delete of arbitrary resources.
	ScopeCredentialPush Scope = "credential-push"
)

// scopeOption is the authorized_keys option marvel writes to record a key's
// scope, for example `marvel-scope="credential-push"`. golang.org/x/crypto/ssh
// parses it into the options list and leaves the key and comment intact.
const scopeOption = "marvel-scope"

// ParseScope validates a scope string. An empty string is admin, since a key
// authorized before scopes existed carries no option.
func ParseScope(s string) (Scope, error) {
	switch Scope(s) {
	case "", ScopeAdmin:
		return ScopeAdmin, nil
	case ScopeCredentialPush:
		return ScopeCredentialPush, nil
	default:
		return "", fmt.Errorf("unknown scope %q (want admin or credential-push)", s)
	}
}

// credentialPushMethods is the exact set a credential-push key may call.
// Naming the set here rather than on the handlers keeps the authorization
// policy in one place. The credential.* handlers land in a later seam
// (aae-orc-gdum6); calling one before it exists fails as an unknown method,
// never as an authorization bypass. credential.get is deliberately absent: a
// pushing key never reads a value back (reveal is local-socket only, S3).
//
// bus.leaf.connect and bus.leaf.disconnect reuse this exact gate rather than
// growing a new scope: the leaf toggle is an operational sibling of the
// enrollment a credential-push key already performs, and a dedicated grant
// waits on the principal model (aae-orc-bs3x). They are the leaf lifecycle's
// operational verbs, not credential reads, so they converge on the operator or
// supervisor principal when bs3x lands (aae-orc-ct0l4).
var credentialPushMethods = map[string]bool{
	"credential.put":      true,
	"credential.list":     true,
	"credential.delete":   true,
	"bus.leaf.connect":    true,
	"bus.leaf.disconnect": true,
}

// methodAllowedForScope reports whether a caller with the given scope may call
// method. Admin may call anything; credential-push is confined to its set; an
// unrecognized scope may call nothing.
func methodAllowedForScope(method string, scope Scope) bool {
	switch scope {
	case ScopeAdmin:
		return true
	case ScopeCredentialPush:
		return credentialPushMethods[method]
	default:
		return false
	}
}

// scopeFromOptions extracts the marvel-scope value from parsed authorized_keys
// options. An absent option means admin.
func scopeFromOptions(options []string) (Scope, error) {
	for _, o := range options {
		if v, ok := strings.CutPrefix(o, scopeOption+"="); ok {
			return ParseScope(strings.Trim(v, `"`))
		}
	}
	return ScopeAdmin, nil
}

// caller identifies who is making a request, for authorization and for the
// audit fingerprint later handlers record. The local unix socket has an empty
// fingerprint and admin scope; an SSH caller carries the scope and fingerprint
// of the key that authenticated it.
type caller struct {
	scope       Scope
	fingerprint string
	// local is true only for the daemon's own unix socket. An SSH caller is
	// never local, even one holding an admin key. It gates the reveal path
	// (brief 9 S3): a credential value leaves the daemon only to the operator
	// at the local socket, never back out through the mrvl:// tunnel.
	local bool
}

// localCaller is the implicit admin caller for the local unix socket.
func localCaller() caller { return caller{scope: ScopeAdmin, local: true} }

// localOnlyMethods may be called only from the local unix socket, never
// through the mrvl:// tunnel, whatever scope a tunnelled key carries. Keeping
// the set here, beside credentialPushMethods, keeps the authorization policy
// in one place. credential.reveal returns a secret value, so it is confined to
// the operator at the daemon's own socket (brief 9 S3).
var localOnlyMethods = map[string]bool{
	"credential.reveal": true,
}

// methodRequiresLocal reports whether method may be called only from the local
// unix socket.
func methodRequiresLocal(method string) bool {
	return localOnlyMethods[method]
}
