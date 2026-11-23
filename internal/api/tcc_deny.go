package api

// TCCDenied reports whether the default deny of the macOS privacy folders and
// network volumes applies to the role. Absent means yes.
func (r Role) TCCDenied() bool {
	return true
}
