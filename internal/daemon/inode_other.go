//go:build !unix

package daemon

import "io/fs"

// inodeOf is 0 where inodes are not available; a replaced file is then caught
// by its size only.
func inodeOf(fi fs.FileInfo) uint64 { return 0 }
