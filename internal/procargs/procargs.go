// Package procargs reads a process's exact argument list, from the kernel and
// not from `ps`. `ps` prints the arguments as one line, so an empty argument
// vanishes and one with a space splits: `daemon --mrvl ""` reads as
// `daemon --mrvl`, which this build accepts, and the daemon then dies in the
// exec. Linux keeps the list as NUL-separated bytes in /proc/<pid>/cmdline;
// macOS returns it from the kern.procargs2 sysctl. The reexec pre-flight and the
// daemon's start guard both read a daemon's arguments here.
package procargs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// ParseCmdline splits /proc/<pid>/cmdline: each argument ends in NUL, so the
// last NUL closes the last argument and an empty argument is two NULs in a row.
func ParseCmdline(b []byte) ([]string, error) {
	if len(b) == 0 || b[len(b)-1] != 0 {
		return nil, errors.New("the process has no readable argument list")
	}
	args := bytes.Split(b[:len(b)-1], []byte{0})
	if len(args) == 1 && len(args[0]) == 0 {
		return nil, errors.New("the process has no readable argument list")
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = string(a)
	}
	return out, nil
}

// ParseProcargs2 reads kern.procargs2: argc as a native int32, the executable
// path, NUL padding, then argc NUL-terminated arguments, then the environment,
// which is not read. Macs this runs on are little endian.
func ParseProcargs2(b []byte) ([]string, error) {
	if len(b) < 4 {
		return nil, errors.New("kern.procargs2 returned too few bytes")
	}
	argc := int32(binary.LittleEndian.Uint32(b))
	if argc < 1 {
		return nil, fmt.Errorf("kern.procargs2 reports %d arguments", argc)
	}
	rest := b[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil, errors.New("kern.procargs2 has no executable path")
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
	// argc comes from the process being read, so it does not size the result:
	// the buffer cannot hold more arguments than it has bytes.
	out := make([]string, 0, min(int(argc), len(b)))
	for range argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil, errors.New("kern.procargs2 ends inside the arguments")
		}
		out = append(out, string(rest[:end]))
		rest = rest[end+1:]
	}
	return out, nil
}
