package session

// tccDenyBlock renders the sandbox-exec rules that deny the macOS privacy
// folders and network volumes (docs/design/seat-tcc-deny.md).
func tccDenyBlock(home string, mounts, allowBack []string) string {
	return ""
}
