//go:build !unix

package session

import "os"

// ownedByMe has no owner to compare on this platform; the mode check in
// harnessHomeBase is what remains.
func ownedByMe(os.FileInfo) bool { return true }
