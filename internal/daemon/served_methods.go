package daemon

import "sort"

// The sets in this file are views of methodTable (methods.go), filled by
// deriveMethodSets from its init. They replace the five hand-kept lists the
// method names used to live in, and tests and the scope code read them by
// method name.

var (
	// servedMethods is every method the table names. dispatchAs times a call
	// under its own name only when the daemon serves it, so a name a client
	// invents never mints a timing site of its own.
	servedMethods map[string]bool
	// startingMethods answer while the daemon is still adopting tmux state: the
	// reads that say what the daemon is doing, and resume, which ends a hold. A
	// plain stop also answers, through startingAllows, because it must not wait
	// on adoption; stop with teardown stays refused. heartbeat, bus.beat and
	// account.limits get ErrReportDeferred instead of ErrDaemonStarting.
	startingMethods map[string]bool
	// reportDeferredMethods are the reports answered ErrReportDeferred while the
	// daemon is adopting.
	reportDeferredMethods map[string]bool
	// credentialPushMethods is the exact set a credential-push key may call.
	credentialPushMethods map[string]bool
	// localOnlyMethods may be called only from the local unix socket.
	localOnlyMethods map[string]bool
)

// FanOutMethods are the daemon methods a fan-out across clusters may call,
// sorted by name: reads only.
func FanOutMethods() []string {
	var out []string
	for name, s := range methodTable {
		if s.FanOut {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func deriveMethodSets() {
	servedMethods = map[string]bool{}
	startingMethods = map[string]bool{}
	reportDeferredMethods = map[string]bool{}
	credentialPushMethods = map[string]bool{}
	localOnlyMethods = map[string]bool{}
	for name, s := range methodTable {
		servedMethods[name] = true
		if s.Starting {
			startingMethods[name] = true
		}
		if s.Deferred {
			reportDeferredMethods[name] = true
		}
		if s.CredentialPush {
			credentialPushMethods[name] = true
		}
		if s.LocalOnly {
			localOnlyMethods[name] = true
		}
	}
}
