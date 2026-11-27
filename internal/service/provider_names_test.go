package service

import (
	"slices"
	"testing"
)

func TestProviderNamesListsEveryRegisteredProviderSorted(t *testing.T) {
	t.Parallel()
	got := ProviderNames()
	if !slices.Equal(got, []string{ProviderNATSServer}) {
		t.Fatalf("ProviderNames() = %v, want [%s]", got, ProviderNATSServer)
	}
	if !slices.IsSorted(got) {
		t.Errorf("ProviderNames() = %v, want sorted", got)
	}
}

func TestProviderNamesReturnsACopy(t *testing.T) {
	t.Parallel()
	first := ProviderNames()
	if len(first) == 0 {
		t.Fatal("ProviderNames() is empty")
	}
	first[0] = "mutated"
	if second := ProviderNames(); len(second) == 0 || second[0] == "mutated" {
		t.Errorf("a caller's write reached the registry: %v", second)
	}
}
