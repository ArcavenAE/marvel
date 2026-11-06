package usage

import "testing"

// IsAlias is a read-only view of the alias table: a short form is an alias, a
// full id and the empty string are not.
func TestIsAlias(t *testing.T) {
	t.Parallel()
	for model, want := range map[string]bool{
		"opus": true, "sonnet": true, "haiku": true, "fable[1m]": true,
		"claude-opus-5": false, "claude-sonnet-5-5": false, "": false, "gpt-5": false,
	} {
		if got := IsAlias(model); got != want {
			t.Errorf("IsAlias(%q) = %v, want %v", model, got, want)
		}
	}
}
