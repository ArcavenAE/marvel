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

// The twin of TestEveryBackendVariableRedirects, and the test whose absence
// let the two selector lists drift. Every variable that trips the redirect
// classifier must also RESOLVE to a named backend: a variable that redirects
// but resolves to "default" makes the verify command report ok on a session
// that is demonstrably not on the default backend. Drives off the package's
// own slice for the same reason its twin does.
func TestEveryBackendVariableResolvesToANamedBackend(t *testing.T) {
	t.Parallel()
	for _, name := range backendFlagVars {
		got := ResolveBackend(lookupFrom(map[string]string{name: "1"}))
		if got == BackendDefaultName {
			t.Errorf("selector %q resolved to %q, so a session it redirects would verify as being on the default backend", name, got)
		}
		if got == "" {
			t.Errorf("selector %q resolved to the empty backend", name)
		}
	}
}

// The redirect classifier and the resolver must answer over the same set, or
// one session gets two verdicts. This is the invariant B1 broke.
func TestBackendFlagVarsTracksTheSelectorTable(t *testing.T) {
	t.Parallel()
	if len(backendFlagVars) != len(backendSelectorNames) {
		t.Fatalf("backendFlagVars has %d entries, backendSelectorNames %d", len(backendFlagVars), len(backendSelectorNames))
	}
	for i, s := range backendSelectorNames {
		if backendFlagVars[i] != s.env {
			t.Errorf("index %d: flag var %q does not match selector %q", i, backendFlagVars[i], s.env)
		}
	}
}

func TestResolveBackend(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		want Backend
	}{
		{"empty environment resolves to default", nil, BackendDefaultName},
		{"a falsy selector resolves to default", map[string]string{"CLAUDE_CODE_USE_BEDROCK": "0"}, BackendDefaultName},
		{"bedrock selector names bedrock", map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, BackendBedrock},
		{"platform-on-aws selector names anthropic-aws", map[string]string{"CLAUDE_CODE_USE_ANTHROPIC_AWS": "1"}, BackendAnthropicAWS},
		{"vertex selector names vertex", map[string]string{"CLAUDE_CODE_USE_VERTEX": "true"}, BackendVertex},
		{
			// The load-bearing precedence: Bedrock outranks Platform-on-AWS,
			// so both ON resolves to bedrock. This is why the overlay pins
			// competitors OFF rather than only turning the chosen one ON.
			name: "bedrock outranks platform-on-aws when both are set",
			env:  map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_USE_ANTHROPIC_AWS": "1"},
			want: BackendBedrock,
		},
		{"a base URL with no selector is custom", map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.internal/v1"}, BackendCustom},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveBackend(lookupFrom(c.env)); got != c.want {
				t.Errorf("ResolveBackend() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBackendMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name               string
		intended, resolved Backend
		want               bool
	}{
		{"no intent matches anything", "", BackendBedrock, true},
		{"bedrock intent matches bedrock", BackendBedrock, BackendBedrock, true},
		{"bedrock intent does not match default", BackendBedrock, BackendDefaultName, false},
		{"subscription intent matches a clean default environment", BackendSubscription, BackendDefaultName, true},
		{
			// The finding-166 silent-redirect: intended subscription, but a
			// leaked selector resolved bedrock. The gate must call this a
			// mismatch.
			name:     "subscription intent does not match a leaked bedrock selector",
			intended: BackendSubscription,
			resolved: BackendBedrock,
			want:     false,
		},
		{"default intent matches default", BackendDefaultName, BackendDefaultName, true},
		{"default intent does not match a leaked selector", BackendDefaultName, BackendBedrock, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := BackendMatches(c.intended, c.resolved); got != c.want {
				t.Errorf("BackendMatches(%q, %q) = %v, want %v", c.intended, c.resolved, got, c.want)
			}
		})
	}
}

// The list names the bearers known today; the suffixes refuse the next one
// before anyone remembers to add it (ADR-009). Near-misses stay allowed: a
// helper pointer, a selector, a token-count setting.
func TestBackendBearerEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key  string
		want bool
	}{
		{"ANTHROPIC_API_KEY", true},
		{"ANTHROPIC_AUTH_TOKEN", true},
		{"CLAUDE_CODE_OAUTH_TOKEN", true},
		{"AWS_BEARER_TOKEN_BEDROCK", true},
		{"AWS_SECRET_ACCESS_KEY", true},
		{"AWS_SESSION_TOKEN", true},
		{"OPENAI_API_KEY", true},
		{"SOME_ROUTER_AUTH_TOKEN", true},
		{"MARVEL_BACKEND_API_KEY_HELPER", false},
		{"AWS_PROFILE", false},
		{"ANTHROPIC_BASE_URL", false},
		{"CLAUDE_CODE_USE_BEDROCK", false},
		{"CLAUDE_CODE_MAX_OUTPUT_TOKENS", false},
	}
	for _, tt := range tests {
		if got := BackendBearerEnv(tt.key); got != tt.want {
			t.Errorf("BackendBearerEnv(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}
