package api

// Spec states a session can carry against its role's current runtime
// (docs/design/drift-view.md section 2). Empty means there is no role to
// compare against.
const (
	SpecCurrent = "current"
	SpecBehind  = "behind"
)

// RuntimeDrift names the Runtime fields that differ between a session's
// stored runtime and its role's current one.
func RuntimeDrift(session, role Runtime) []string {
	return nil
}
