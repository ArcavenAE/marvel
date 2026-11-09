package session

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The usage accountant is told where a seat keeps its model catalog only when
// the seat has a private home, and only for opencode.
func TestSeatCacheHome(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, harness, home, want string
	}{
		{"opencode with a private home", "opencode", "/h/seat", "/h/seat/cache"},
		{"opencode sharing the home", "opencode", "", ""},
		{"codex with a private home", "codex", "/h/seat", ""},
		{"goose with a private home", "goose", "/h/seat", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			sess := &api.Session{HarnessHome: c.home, Runtime: api.Runtime{Name: c.harness}}
			if got := seatCacheHome(sess); got != c.want {
				t.Errorf("seatCacheHome = %q, want %q", got, c.want)
			}
		})
	}
}
