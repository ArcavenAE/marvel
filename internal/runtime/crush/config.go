// Package crush holds marvel's guards for launching the Crush harness. It
// carries Guard 1 of finding-033: refuse a launch whose workspace holds a
// Crush config file the operator has not approved, because Crush runs every
// one of them as trusted code.
//
// Nothing calls it yet. The Crush adapter (aae-orc-6c2r) does not exist, and
// until it lands this package is dead code that a test suite exercises.
// Guards 0 and 2 (the observability socket stays off, and no credential rides
// in the spawn environment) belong to the adapter and land with it.
package crush

// PinnedTag is the Crush release whose config search this package copies. Its
// walk was read at this tag and found unchanged at v0.98.1. A later release
// can add a config name or move the walk boundary, and either one is a bypass,
// so the adapter must re-read lookupConfigs before it supports a new release.
const PinnedTag = "v0.88.1"

// ConfigNames are the project config files Crush looks for in each directory,
// highest priority first. All four are executable: crushrc and .crushrc run in
// Crush's shell interpreter, and the JSON files are trusted code too
// (crush#3410).
var ConfigNames = []string{".crushrc", "crushrc", ".crush.json", "crush.json"}

// Reason says why a config file was refused. The empty Reason is approval.
type Reason string

const (
	// ReasonUnapproved is a config file with no entry in the approved set.
	ReasonUnapproved Reason = "unapproved"
	// ReasonChanged is a config file whose hash differs from the approved one.
	ReasonChanged Reason = "changed"
	// ReasonSymlink is a config file that is a symbolic link. It is refused
	// whatever it points at, since the target can be swapped after approval.
	ReasonSymlink Reason = "symlink"
	// ReasonNotRegular is a config file that is not a regular file.
	ReasonNotRegular Reason = "not-regular"
)

// File is one config file the scan found.
type File struct {
	// Rel is the file's path relative to the boundary, slash separated, with
	// the name as it is spelled on disk. It is the key into the approved set.
	Rel string
	// SHA256 is the hex digest of the file's bytes, empty for a refused link
	// or non-regular file, which are never read.
	SHA256 string
	// Reason is empty when the file is approved.
	Reason Reason
}

// Verdict is what a scan found.
type Verdict struct {
	Files []File
}

// OK reports whether every config file found is approved. A scan that found
// none is OK.
func (v Verdict) OK() bool { return false }

// Refused returns the files that are not approved.
func (v Verdict) Refused() []File { return nil }

// Boundary returns the directory Crush stops its upward config search at: the
// git working tree root of cwd, or cwd itself when git cannot name one.
func Boundary(cwd string) (string, error) { return "", nil }

// InspectConfig scans cwd and each ancestor up to and including boundary for
// Crush config files, and checks each against approved, a map from Rel to the
// hex sha256 the operator vouched for. An empty boundary scans cwd only. An
// error means the scan could not finish, and the caller must refuse the
// launch.
func InspectConfig(cwd, boundary string, approved map[string]string) (Verdict, error) {
	return Verdict{}, nil
}
