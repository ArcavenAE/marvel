package api

import "testing"

// lookupFrom builds a lookup over a fixed map, so a test states exactly the
// environment it means and nothing leaks in from the process.
func lookupFrom(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestClassifyBackendRedirection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		want BackendRedirection
	}{
		{
			name: "empty environment is the vendor default",
			env:  nil,
			want: BackendDefault,
		},
		{
			name: "a falsy flag is still the default",
			env:  map[string]string{"CLAUDE_CODE_USE_BEDROCK": "0"},
			want: BackendDefault,
		},
		{
			name: "the empty string is not a redirect",
			env:  map[string]string{"ANTHROPIC_BASE_URL": ""},
			want: BackendDefault,
		},
		{
			name: "whitespace-only value is not a redirect",
			env:  map[string]string{"ANTHROPIC_BASE_URL": "   "},
			want: BackendDefault,
		},
		{
			name: "bedrock flag set to 1 redirects",
			env:  map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"},
			want: BackendRedirected,
		},
		{
			name: "vertex flag set to true redirects",
			env:  map[string]string{"CLAUDE_CODE_USE_VERTEX": "true"},
			want: BackendRedirected,
		},
		{
			name: "an off-spelled flag is not a redirect",
			env:  map[string]string{"CLAUDE_CODE_USE_VERTEX": "off"},
			want: BackendDefault,
		},
		{
			name: "a proxy base URL redirects",
			env:  map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.internal/v1"},
			want: BackendRedirected,
		},
		{
			name: "a bedrock service tier redirects",
			env:  map[string]string{"ANTHROPIC_BEDROCK_SERVICE_TIER": "flex"},
			want: BackendRedirected,
		},
		{
			name: "the gateway flag redirects",
			env:  map[string]string{"CLAUDE_CODE_USE_GATEWAY": "yes"},
			want: BackendRedirected,
		},
		{
			name: "an unrelated variable does not redirect",
			env:  map[string]string{"ANTHROPIC_MODEL": "claude-opus-4-8"},
			want: BackendDefault,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyBackendRedirection(lookupFrom(c.env)); got != c.want {
				t.Errorf("ClassifyBackendRedirection(%v) = %q, want %q", c.env, got, c.want)
			}
		})
	}
}

// The classifier never invents the "cannot tell" verdict: that state is the
// zero value, reserved for sessions the classifier was never run on.
func TestClassifyNeverReturnsUnknown(t *testing.T) {
	t.Parallel()
	for _, env := range []map[string]string{
		nil,
		{"CLAUDE_CODE_USE_BEDROCK": "1"},
		{"ANTHROPIC_BASE_URL": "https://x"},
	} {
		if got := ClassifyBackendRedirection(lookupFrom(env)); got == BackendUnknown {
			t.Errorf("ClassifyBackendRedirection(%v) returned the zero value; it must decide default or redirected", env)
		}
	}
}

// Every backend-selecting variable finding-016 axis 4 names must trip the
// classifier, so adding one to the list is what wires it in. A hand-copied
// second list here would be the same omission the check is meant to catch,
// so it drives off the package's own slices.
func TestEveryBackendVariableRedirects(t *testing.T) {
	t.Parallel()
	for _, name := range backendFlagVars {
		if got := ClassifyBackendRedirection(lookupFrom(map[string]string{name: "1"})); got != BackendRedirected {
			t.Errorf("flag %q set truthy did not redirect: got %q", name, got)
		}
	}
	for _, name := range backendValueVars {
		if got := ClassifyBackendRedirection(lookupFrom(map[string]string{name: "set"})); got != BackendRedirected {
			t.Errorf("value var %q set did not redirect: got %q", name, got)
		}
	}
}
