package api

// TCCDenied reports whether the default deny of the macOS privacy folders and
// network volumes applies to the role. Absent means yes; only an explicit
// tcc_deny = false turns it off (docs/design/seat-tcc-deny.md).
func (r Role) TCCDenied() bool {
	return r.TCCDeny == nil || *r.TCCDeny
}
