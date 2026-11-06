//go:build darwin

package hostvitals

import (
	"encoding/binary"
	"errors"
	"testing"
)

func swapUsageBytes(total, avail, used uint64) []byte {
	b := make([]byte, 32)
	binary.NativeEndian.PutUint64(b[0:], total)
	binary.NativeEndian.PutUint64(b[8:], avail)
	binary.NativeEndian.PutUint64(b[16:], used)
	return b
}

func TestReadWithTheKernelFigures(t *testing.T) {
	raw := func(name string, _ ...int) ([]byte, error) {
		if name != "vm.swapusage" {
			t.Errorf("raw sysctl %q, want vm.swapusage", name)
		}
		return swapUsageBytes(2048<<20, 1585<<20, 463<<20), nil
	}
	u32 := func(name string) (uint32, error) {
		switch name {
		case "kern.memorystatus_vm_pressure_level":
			return 2, nil
		case "kern.memorystatus_level":
			return 87, nil
		}
		return 0, errors.New("unexpected " + name)
	}
	r := readWith(raw, u32)
	want := Reading{
		SwapOK: true, SwapUsed: 463 << 20, SwapTotal: 2048 << 20,
		KernelPressureOK: true, PressureLevel: 2, MemoryFreePct: 87,
	}
	if r != want {
		t.Errorf("readWith = %+v, want %+v", r, want)
	}
}

func TestReadWithLeavesAGapForWhatTheKernelRefuses(t *testing.T) {
	fail := errors.New("sysctl: refused")
	r := readWith(
		func(string, ...int) ([]byte, error) { return nil, fail },
		func(string) (uint32, error) { return 0, fail },
	)
	if r != (Reading{}) {
		t.Errorf("readWith on refusal = %+v, want the zero reading", r)
	}
	short := readWith(
		func(string, ...int) ([]byte, error) { return []byte{1, 2, 3}, nil },
		func(string) (uint32, error) { return 1, nil },
	)
	if short.SwapOK {
		t.Errorf("a short swapusage struct read as %+v", short)
	}
}

// Read is the real host: this one asks the kernel, in process.
func TestReadOnThisHost(t *testing.T) {
	r := Read()
	if !r.SwapOK || !r.KernelPressureOK {
		t.Errorf("Read() = %+v, want swap and kernel pressure readings", r)
	}
	if r.PressureLevel < 1 {
		t.Errorf("pressure level %d, want 1 or more", r.PressureLevel)
	}
}
