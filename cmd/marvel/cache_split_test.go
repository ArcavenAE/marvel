package main

import (
	"reflect"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// I-477 slice 2, the client half (card i477c1113-1): TIN, CR and CW are the
// classes of PROMPT, HIT% is CR over PROMPT, and a class nobody reported prints
// "-" where a metered zero prints "0".

func cacheSession(fresh, read, write, prompt *int, partial bool) api.Session {
	s := api.Session{Name: "seat", Workspace: "ws", State: api.SessionRunning}
	s.SpendFresh, s.SpendCacheRead, s.SpendCacheWrite, s.SpendPromptTokens = fresh, read, write, prompt
	s.SpendPartial = partial
	return s
}

func num(n int) *int { return &n }

func TestUnreportedClassRendersDashNotZero(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    api.Session
		want []string // name, TIN, CR, CW, PROMPT
	}{
		{"all reported", cacheSession(num(500), num(300), num(100), num(900), false), []string{"seat", "500", "300", "100", "900"}},
		{"metered zeros", cacheSession(num(100), num(0), num(0), num(100), false), []string{"seat", "100", "0", "0", "100"}},
		{"codex reports no cache write", cacheSession(num(500), num(300), nil, num(800), false), []string{"seat", "500", "300", "-", "800"}},
		{"nothing metered", cacheSession(nil, nil, nil, nil, false), []string{"seat", "-", "-", "-", "-"}},
		{"partial sum is a lower bound", cacheSession(num(500), num(300), num(100), num(900), true), []string{"seat", "500+", "300+", "100+", "900+"}},
		{"partial dash stays a dash", cacheSession(num(500), num(300), nil, num(800), true), []string{"seat", "500+", "300+", "-", "800+"}},
	} {
		got := tokenCells(t, "name,tin,cr,cw,prompt", tc.s)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: name,tin,cr,cw,prompt = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHitPctDashWhenPromptUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    api.Session
		want string
	}{
		{"known", cacheSession(num(500), num(300), num(100), num(900), false), "33%"},
		{"all cache", cacheSession(num(0), num(800), num(0), num(800), false), "100%"},
		{"no cache read", cacheSession(num(800), num(0), num(0), num(800), false), "0%"},
		{"prompt unknown", cacheSession(nil, num(300), nil, nil, false), "-"},
		{"prompt zero", cacheSession(num(0), num(0), num(0), num(0), false), "-"},
		{"cache read unreported", cacheSession(num(800), nil, nil, num(800), false), "-"},
		{"partial sums give a ratio of the same records", cacheSession(num(500), num(300), num(100), num(900), true), "33%"},
	} {
		got := tokenCells(t, "name,hit", tc.s)
		if len(got) != 2 || got[1] != tc.want {
			t.Errorf("%s: HIT%% = %q, want %q", tc.name, got, tc.want)
		}
	}
}
