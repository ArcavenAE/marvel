package main

import (
	"strings"
	"testing"
)

// The hazard itself (marvel#341): `inject <key> Enter` with the default flags.
// Before the --key form this resolved to five literal characters, which on a
// codex approval menu cancels the pending command and leaves the seat reading
// healthy. Each case below asserts the SPECIFIC outcome rather than "an error
// happened", and each refusal is paired with the adjacent form that must still
// work, so a refusal cannot pass by refusing everything.
func TestResolveInjectRefusesABareKeyNameAsText(t *testing.T) {
	t.Parallel()

	// Positive control first. If this fails the refusal below proves nothing,
	// because a resolver that refused every input would also pass.
	control, err := resolveInject("hello", true, nil, true, false, false)
	if err != nil {
		t.Fatalf("control: ordinary text must resolve, got %v", err)
	}
	if len(control) != 1 || control[0].Text != "hello" || !control[0].Literal {
		t.Fatalf("control: want one literal step carrying hello, got %+v", control)
	}

	steps, err := resolveInject("Enter", true, nil, true, false, false)
	if err == nil {
		t.Fatalf("bare Enter as literal text must be refused, got steps %+v", steps)
	}
	for _, want := range []string{
		"is a tmux key name",
		"--key Enter",
		"--enter",
		"--literal",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should name %q so the operator can act on it; got:\n%s", want, err)
		}
	}
}

// Exactness is what keeps the guard from becoming a nuisance: the refusal is
// for text that IS a key name, not text that mentions one.
func TestResolveInjectOnlyRefusesAnExactKeyName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		text    string
		refused bool
	}{
		{"bare Enter", "Enter", true},
		{"bare Escape", "Escape", true},
		{"control key", "C-c", true},
		{"function key", "F5", true},
		{"a sentence that mentions Enter", "Enter your name", false},
		{"a word containing it", "Entertain", false},
		{"lower case is not a tmux key", "enter", false},
		{"empty draft submit", "", false},
		{"not a modifier form", "C-", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveInject(tt.text, true, nil, true, false, false)
			if tt.refused && err == nil {
				t.Fatalf("%q should be refused", tt.text)
			}
			if !tt.refused && err != nil {
				t.Fatalf("%q should pass through as ordinary text, got %v", tt.text, err)
			}
		})
	}
}

// An operator who states literal intent is believed. The trap is the default,
// not the flag, so the guard must not fire once --literal is explicit.
func TestResolveInjectBelievesAnExplicitLiteralFlag(t *testing.T) {
	t.Parallel()

	if _, err := resolveInject("Enter", true, nil, true, false, false); err == nil {
		t.Fatalf("control: the default must still refuse, or this test proves nothing")
	}

	steps, err := resolveInject("Enter", true, nil, true, false, true)
	if err != nil {
		t.Fatalf("explicit --literal must be honoured, got %v", err)
	}
	if len(steps) != 1 || steps[0].Text != "Enter" || !steps[0].Literal || steps[0].Enter {
		t.Fatalf("want one literal step carrying Enter, got %+v", steps)
	}
}

func TestResolveInjectKeyForm(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		text  string
		has   bool
		keys  []string
		enter bool
		want  []injectStep
	}{
		{
			name: "bare wake presses the key and sends no text",
			keys: []string{"Enter"},
			want: []injectStep{{Text: "Enter", Literal: false}},
		},
		{
			name: "text then key is two steps in order",
			text: "hello", has: true, keys: []string{"Enter"},
			want: []injectStep{
				{Text: "hello", Literal: true, Enter: false},
				{Text: "Enter", Literal: false},
			},
		},
		{
			name: "repeatable, order preserved",
			keys: []string{"C-c", "Enter"},
			want: []injectStep{
				{Text: "C-c", Literal: false},
				{Text: "Enter", Literal: false},
			},
		},
		{
			name: "the existing submit form is untouched",
			text: "", has: true, enter: true,
			want: []injectStep{{Text: "", Literal: true, Enter: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveInject(tt.text, tt.has, tt.keys, true, tt.enter, false)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("want %d step(s), got %d: %+v", len(tt.want), len(got), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("step %d: want %+v, got %+v", i, tt.want[i], got[i])
				}
			}
		})
	}
}

func TestResolveInjectRefusesAmbiguousAndEmptyForms(t *testing.T) {
	t.Parallel()

	if _, err := resolveInject("", false, []string{"Enter"}, true, false, false); err != nil {
		t.Fatalf("control: --key alone must resolve, got %v", err)
	}

	if _, err := resolveInject("", false, nil, true, false, false); err == nil {
		t.Error("neither text nor --key should be refused")
	} else if !strings.Contains(err.Error(), "nothing to send") {
		t.Errorf("want a nothing-to-send refusal, got: %v", err)
	}

	if _, err := resolveInject("hi", true, []string{"Enter"}, true, true, false); err == nil {
		t.Error("--enter with --key is ambiguous and should be refused")
	} else if !strings.Contains(err.Error(), "both append a keystroke") {
		t.Errorf("want an ambiguity refusal, got: %v", err)
	}

	if _, err := resolveInject("", false, []string{"  "}, true, false, false); err == nil {
		t.Error("a blank --key should be refused")
	}
}

func TestDescribeInject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		steps []injectStep
		want  string
	}{
		{"key only", []injectStep{{Text: "Enter"}}, "injected Enter into s1"},
		{"text only", []injectStep{{Text: "hello", Literal: true}}, "injected 5 bytes into s1"},
		{
			"text and key",
			[]injectStep{{Text: "hello", Literal: true}, {Text: "Enter"}},
			"injected 5 bytes and Enter into s1",
		},
		{
			// An empty literal send is a no-op at the pane, so the Enter is
			// the whole action and naming zero bytes beside it would be
			// noise. `inject <key> '' --enter` and `inject <key> --key Enter`
			// have the same effect and read the same.
			"submit form reports the Enter alone",
			[]injectStep{{Text: "", Literal: true, Enter: true}},
			"injected Enter into s1",
		},
		{
			"an empty send with no key still reports honestly",
			[]injectStep{{Text: "", Literal: true}},
			"injected 0 bytes into s1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := describeInject("s1", tt.steps); got != tt.want {
				t.Errorf("want %q, got %q", tt.want, got)
			}
		})
	}
}
