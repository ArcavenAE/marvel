package service

import (
	"context"
	"testing"
)

type fakeProvider struct{ calls []string }

func (f *fakeProvider) Start(context.Context) error { f.calls = append(f.calls, "start"); return nil }

func (f *fakeProvider) Stop(keep bool) {
	f.calls = append(f.calls, map[bool]string{true: "stop-keep", false: "stop"}[keep])
}

func (f *fakeProvider) Restart(reason string) error {
	f.calls = append(f.calls, "restart:"+reason)
	return nil
}

func (f *fakeProvider) Reload() error  { f.calls = append(f.calls, "reload"); return nil }
func (f *fakeProvider) Status() Status { return Status{State: StateUp, Ready: true} }

type fakeFingerprinter struct{ fakeProvider }

func (fakeFingerprinter) EnvFingerprint(key string) (string, bool) { return "fp-" + key, key != "" }

var (
	_ Lifecycle        = (*fakeProvider)(nil)
	_ Lifecycle        = (*fakeFingerprinter)(nil)
	_ EnvFingerprinter = (*fakeFingerprinter)(nil)
)

// A provider with no environment value to report is still a Lifecycle, and the
// daemon finds the optional interface by type assertion.
func TestEnvFingerprinterIsOptional(t *testing.T) {
	var plain Lifecycle = &fakeProvider{}
	if _, ok := plain.(EnvFingerprinter); ok {
		t.Error("a provider with no fingerprint method satisfied EnvFingerprinter")
	}
	var with Lifecycle = &fakeFingerprinter{}
	ef, ok := with.(EnvFingerprinter)
	if !ok {
		t.Fatal("a provider with the method did not satisfy EnvFingerprinter")
	}
	if fp, set := ef.EnvFingerprint("k"); fp != "fp-k" || !set {
		t.Errorf("EnvFingerprint = %q, %v", fp, set)
	}
}

// The state words are read by operators and tests; changing one is a visible change.
func TestStateWordsAreStable(t *testing.T) {
	for state, want := range map[State]string{StateUp: "up", StateDown: "down", StateBackoff: "backoff"} {
		if string(state) != want {
			t.Errorf("state %q, want %q", state, want)
		}
	}
}
