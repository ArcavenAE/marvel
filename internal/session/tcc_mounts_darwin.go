package session

import "syscall"

// networkMounts lists the mount points of network file systems, read from the
// host's mount table now. A read that fails returns what it has.
func networkMounts() []string {
	n, err := syscall.Getfsstat(nil, 2) // MNT_NOWAIT
	if err != nil || n <= 0 {
		return nil
	}
	buf := make([]syscall.Statfs_t, n)
	n, err = syscall.Getfsstat(buf, 2)
	if err != nil {
		return nil
	}
	var out []string
	for _, fs := range buf[:n] {
		if isNetworkFS(cString(fs.Fstypename[:])) {
			out = append(out, cString(fs.Mntonname[:]))
		}
	}
	return out
}

func cString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}
