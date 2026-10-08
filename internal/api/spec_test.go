package api

import (
	"reflect"
	"testing"
)

func fullRuntime() Runtime {
	return Runtime{
		Name: "claude", Command: "claude", Args: []string{"--a", "--b"},
		Script: "s.sh", Mode: RuntimeModeHeadless, Prompt: "do it",
		ContextWindow: 200000, ContextFeed: ContextFeedStatusline,
		Env: map[string]string{"K": "v"}, Backend: "bedrock",
	}
}

// Every field of Runtime counts, and the result names the field and never its
// value (docs/design/drift-view.md section 2 and 3).
func TestRuntimeDriftNamesEachDifferingField(t *testing.T) {
	for _, tc := range []struct {
		field  string
		mutate func(*Runtime)
	}{
		{"name", func(r *Runtime) { r.Name = "other" }},
		{"command", func(r *Runtime) { r.Command = "other" }},
		{"args", func(r *Runtime) { r.Args = []string{"--a"} }},
		{"script", func(r *Runtime) { r.Script = "t.sh" }},
		{"mode", func(r *Runtime) { r.Mode = RuntimeModeInteractive }},
		{"prompt", func(r *Runtime) { r.Prompt = "other" }},
		{"context_window", func(r *Runtime) { r.ContextWindow = 1 }},
		{"context_feed", func(r *Runtime) { r.ContextFeed = "" }},
		{"env", func(r *Runtime) { r.Env = map[string]string{"K": "w"} }},
		{"backend", func(r *Runtime) { r.Backend = "default" }},
	} {
		role := fullRuntime()
		tc.mutate(&role)
		got := RuntimeDrift(fullRuntime(), role)
		if !reflect.DeepEqual(got, []string{tc.field}) {
			t.Errorf("%s changed: drift = %v, want [%s]", tc.field, got, tc.field)
		}
	}
}

func TestRuntimeDriftOfEqualRuntimesIsEmpty(t *testing.T) {
	if got := RuntimeDrift(fullRuntime(), fullRuntime()); len(got) != 0 {
		t.Fatalf("equal runtimes drift = %v", got)
	}
}

// Args order matters: the same words in another order are a different command line.
func TestRuntimeDriftArgsOrderMatters(t *testing.T) {
	role := fullRuntime()
	role.Args = []string{"--b", "--a"}
	if got := RuntimeDrift(fullRuntime(), role); !reflect.DeepEqual(got, []string{"args"}) {
		t.Fatalf("reordered args drift = %v, want [args]", got)
	}
}

// A nil and an empty slice or map are the same absence.
func TestRuntimeDriftNilAndEmptyAreEqual(t *testing.T) {
	a := Runtime{Command: "sleep"}
	b := Runtime{Command: "sleep", Args: []string{}, Env: map[string]string{}}
	if got := RuntimeDrift(a, b); len(got) != 0 {
		t.Fatalf("nil versus empty drift = %v", got)
	}
	if got := RuntimeDrift(b, a); len(got) != 0 {
		t.Fatalf("empty versus nil drift = %v", got)
	}
}

// Several differing fields come back in the Runtime's own field order.
func TestRuntimeDriftListsFieldsInStructOrder(t *testing.T) {
	role := fullRuntime()
	role.Prompt = "other"
	role.Args = nil
	role.Backend = "default"
	want := []string{"args", "prompt", "backend"}
	if got := RuntimeDrift(fullRuntime(), role); !reflect.DeepEqual(got, want) {
		t.Fatalf("drift = %v, want %v", got, want)
	}
}

func TestRuntimeDriftNeverCarriesAValue(t *testing.T) {
	role := fullRuntime()
	role.Env = map[string]string{"K": "secret-value"}
	role.Prompt = "secret-prompt"
	for _, name := range RuntimeDrift(fullRuntime(), role) {
		if name == "secret-value" || name == "secret-prompt" {
			t.Fatalf("drift carries a value: %q", name)
		}
	}
}
