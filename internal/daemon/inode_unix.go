//go:build unix

package daemon

import (
	"io/fs"
	"syscall"
)

// inodeOf is the file's inode, 0 where it cannot be read.
func inodeOf(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
