package procargs

// Marker is the part of a process's environment that says whether it belongs to
// marvel, and nothing else. The host vitals sampler reads it for a process that
// has lost its parent, to tell a marvel orphan from any other launchd child
// (docs/design/host-vitals.md section 2.1). Reading another process's
// environment is the same exposure as reading its arguments, so the parsers
// here keep two fields and drop every other byte as they go: the value of
// MARVEL_SESSION, and a flag for a MARVEL_SOCKET or MARVEL_HOME that names a
// marvel test or harness path. No other variable, and no other MARVEL_* value,
// is ever held, returned or logged.
type Marker struct {
	// Session is the value of MARVEL_SESSION, cut at MaxSessionLen bytes.
	Session string
	// MarvelPath is true when MARVEL_SOCKET or MARVEL_HOME names a marvel test
	// or harness path (MarvelPathMarks).
	MarvelPath bool
}

// MaxSessionLen caps the MARVEL_SESSION value a Marker keeps.
const MaxSessionLen = 128

// MarvelPathMarks are the path fragments that make a MARVEL_SOCKET or
// MARVEL_HOME a marvel test or harness path: a test's scratch home and a
// harness home.
var MarvelPathMarks = []string{"marvel-test-home-", "marvel-h-", "marvel-harness-homes"}

// Marvel reports whether the process carries any marvel marker.
func (m Marker) Marvel() bool { return m.Session != "" || m.MarvelPath }

// ParseProcargs2Marker reads the marker out of a kern.procargs2 buffer: argc,
// the executable path, the arguments, then the environment. It keeps only the
// Marker.
func ParseProcargs2Marker(b []byte) (Marker, error) {
	return Marker{}, nil
}

// ParseEnviron reads the marker out of /proc/<pid>/environ, NUL separated.
func ParseEnviron(b []byte) Marker {
	return Marker{}
}
