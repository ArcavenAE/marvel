package session

// isNetworkFS reports whether a file system type is one macOS asks a privacy
// question about: NFS, SMB, AFP and WebDAV.
func isNetworkFS(fstype string) bool {
	switch fstype {
	case "nfs", "smbfs", "afpfs", "webdav":
		return true
	}
	return false
}
