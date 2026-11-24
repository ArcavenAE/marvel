package session

// ComposeRunProfile joins the three parts of a sandboxed run's profile into one
// text: the role's own profile, then the requested run profile, then the deny
// block, with the block last so a later rule in the request cannot lift it
// (docs/design/seat-tcc-deny.md, sections 5 and 14).
func ComposeRunProfile(role, requested, block string) string {
	return "" // red: not built
}
