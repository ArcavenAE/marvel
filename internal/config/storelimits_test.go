package config

import (
	"errors"
	"strings"
	"testing"
)

func TestParseStoreSize(t *testing.T) {
	t.Parallel()
	good := map[string]int64{
		"1048576": 1048576,
		"64KiB":   64 * 1024,
		"512MiB":  512 << 20,
		"2GiB":    2 << 30,
		"1TiB":    1 << 40,
		" 2GiB ":  2 << 30,
	}
	for in, want := range good {
		got, err := ParseStoreSize(in)
		if err != nil || got != want {
			t.Errorf("ParseStoreSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	bad := []string{"", "0", "-1", "abc", "2GB", "2G", "1.5GiB", "9999999TiB", "GiB", "2 GiB x"}
	for _, in := range bad {
		if got, err := ParseStoreSize(in); err == nil {
			t.Errorf("ParseStoreSize(%q) = %d, nil; want an error", in, got)
		}
	}
	// A refusal teaches the spelling: 2GB is ambiguous between 10^9 and 2^30.
	if _, err := ParseStoreSize("2GB"); err == nil || !strings.Contains(err.Error(), "GiB") {
		t.Errorf("ParseStoreSize(2GB) error %v does not point at the binary units", err)
	}
}

func TestValidateBusChecksStoreLimits(t *testing.T) {
	t.Parallel()
	ok := &Bus{Managed: true, Listen: "127.0.0.1:4222", MaxFileStore: "2GiB", MaxMemoryStore: "256MiB"}
	if err := ValidateBus("kinu", ok); err != nil {
		t.Fatalf("valid limits refused: %v", err)
	}
	for _, b := range []*Bus{
		{Managed: true, Listen: "127.0.0.1:4222", MaxFileStore: "lots"},
		{Managed: true, Listen: "127.0.0.1:4222", MaxMemoryStore: "2GB"},
		{URL: "nats://127.0.0.1:4222", MaxFileStore: "2GiB"},
	} {
		err := ValidateBus("kinu", b)
		if !errors.Is(err, ErrInvalidBus) {
			t.Errorf("ValidateBus(%+v) = %v, want ErrInvalidBus", b, err)
		}
	}
}

func TestResolveCarriesStoreLimitsInBytes(t *testing.T) {
	t.Parallel()
	b := &Bus{Managed: true, Listen: "127.0.0.1:4222", MaxFileStore: "2GiB", MaxMemoryStore: "256MiB"}
	r := b.Resolve("/state")
	if r.MaxFileStore != 2<<30 || r.MaxMemoryStore != 256<<20 {
		t.Errorf("resolved limits = %d, %d; want %d, %d", r.MaxFileStore, r.MaxMemoryStore, int64(2<<30), int64(256<<20))
	}
	none := (&Bus{Managed: true, Listen: "127.0.0.1:4222"}).Resolve("/state")
	if none.MaxFileStore != 0 || none.MaxMemoryStore != 0 {
		t.Errorf("unset limits resolved to %d, %d; want 0, 0", none.MaxFileStore, none.MaxMemoryStore)
	}
}
