//go:build linux

package hostvitals

import "os"

// Read takes one reading of this host from /proc. Each file is read in the
// daemon's own process; nothing is exec'd.
func Read() Reading {
	r := readFrom("/proc/meminfo", "/proc/pressure/memory")
	readExtras(&r, "/proc/meminfo", "/proc/vmstat")
	return r
}

// readExtras adds physical memory (MemTotal) and the cumulative swap-in count
// (pswpin, in pages). A file that is absent or does not parse leaves its field
// zero.
func readExtras(r *Reading, meminfo, vmstat string) {
	if b, err := os.ReadFile(meminfo); err == nil {
		r.MemTotal = parseMeminfoTotal(string(b))
	}
	if b, err := os.ReadFile(vmstat); err == nil {
		r.SwapIns, r.SwapInsOK = parseVmstatSwapIns(string(b))
	}
}

// readFrom reads the two files and leaves a group zero when its file is
// absent or does not parse. /proc/pressure/memory is absent on a kernel built
// without PSI.
func readFrom(meminfo, psi string) Reading {
	var r Reading
	if b, err := os.ReadFile(meminfo); err == nil {
		r.SwapUsed, r.SwapTotal, r.SwapOK = parseMeminfoSwap(string(b))
	}
	if b, err := os.ReadFile(psi); err == nil {
		r.PSISomeAvg10, r.PSIOK = parsePSISomeAvg10(string(b))
	}
	return r
}
