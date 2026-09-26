//go:build unix

package session

import (
	"os"
	"syscall"
)

// ownedByMe reports whether a file belongs to the effective user.
func ownedByMe(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
