//go:build darwin

package hostvitals

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// Read takes one reading of this host by sysctl in the daemon's own process.
// Nothing is exec'd: the figures are the ones `sysctl vm.swapusage` and
// `memory_pressure` print.
func Read() Reading { return readWith(unix.SysctlRaw, unix.SysctlUint32) }

// readWith is Read with the two sysctl calls passed in, so a test can feed it.
func readWith(raw func(string, ...int) ([]byte, error), u32 func(string) (uint32, error)) Reading {
	var r Reading
	if b, err := raw("vm.swapusage"); err == nil {
		r.SwapUsed, r.SwapTotal, r.SwapOK = parseSwapUsage(b)
	}
	lvl, errL := u32("kern.memorystatus_vm_pressure_level")
	free, errF := u32("kern.memorystatus_level")
	if errL == nil && errF == nil && free <= 100 {
		r.KernelPressureOK = true
		r.PressureLevel = int(lvl)
		r.MemoryFreePct = int(free)
	}
	return r
}

// parseSwapUsage reads the xsw_usage struct that vm.swapusage returns: total,
// available and used as three native-endian uint64, then a page size.
func parseSwapUsage(b []byte) (used, total uint64, ok bool) {
	if len(b) < 24 {
		return 0, 0, false
	}
	total = binary.NativeEndian.Uint64(b[0:8])
	used = binary.NativeEndian.Uint64(b[16:24])
	if used > total {
		return 0, 0, false
	}
	return used, total, true
}
