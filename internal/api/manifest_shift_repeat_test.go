package api

import (
	"strings"
	"testing"
	"time"
)

// handoff_repeat_flat and handoff_repeat_ceiling on the shift block set how an
// escalated request's repeats back off (marvel#1381). Written in TOML and YAML
// because the two decoders drop unknown keys differently.

func TestHandoffRepeatKeysParseOnTheShiftBlock(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"toml": shiftTOML("", viaMaxAgeTOML+pairTOML+"    handoff_repeat_flat = 5\n    handoff_repeat_ceiling = \"6h\"\n"),
		"yaml": shiftYAML("", viaMaxAgeYAML+pairYAML+"          handoff_repeat_flat: 5\n          handoff_repeat_ceiling: 6h\n"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := applyShift(t, src)
			if p == nil || p.HandoffRepeatFlat != 5 || p.HandoffRepeatCeiling != 6*time.Hour {
				t.Fatalf("policy = %+v, want flat 5 and ceiling 6h", p)
			}
		})
	}
}

func TestHandoffRepeatDefaultsWhenUnset(t *testing.T) {
	t.Parallel()
	p := applyShift(t, shiftTOML("", viaMaxAgeTOML+pairTOML))
	if p == nil {
		t.Fatal("no policy")
	}
	if p.HandoffRepeatFlat != 0 || p.HandoffRepeatCeiling != 0 {
		t.Fatalf("unset keys were stored as %d and %s; the zero value means the default", p.HandoffRepeatFlat, p.HandoffRepeatCeiling)
	}
	if got := p.HandoffRepeatFlatOrDefault(); got != 3 {
		t.Errorf("flat default = %d, want 3", got)
	}
	if got := p.HandoffRepeatCeilingOrDefault(); got != 24*time.Hour {
		t.Errorf("ceiling default = %s, want 24h", got)
	}
}

func TestHandoffRepeatKeysRefuseNonsense(t *testing.T) {
	t.Parallel()
	for name, extra := range map[string]string{
		"flat zero":        "    handoff_repeat_flat = 0\n",
		"flat negative":    "    handoff_repeat_flat = -2\n",
		"ceiling zero":     "    handoff_repeat_ceiling = \"0s\"\n",
		"ceiling negative": "    handoff_repeat_ceiling = \"-1h\"\n",
		"ceiling garbage":  "    handoff_repeat_ceiling = \"tomorrow\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := applyShiftErr(t, shiftTOML("", viaMaxAgeTOML+pairTOML+extra))
			if err == nil || !strings.Contains(err.Error(), "handoff_repeat") {
				t.Fatalf("err = %v, want it to name the handoff_repeat key", err)
			}
		})
	}
}

// The gap after the delivered-th send: the escalation and the flat repeats go
// one window apart, then the gap doubles up to the ceiling.
func TestHandoffRepeatGap(t *testing.T) {
	t.Parallel()
	const w = 25 * time.Minute
	def := &ShiftPolicy{HandoffWindow: w}
	for _, tc := range []struct {
		name      string
		p         *ShiftPolicy
		delivered int
		want      time.Duration
	}{
		{"first send", def, 0, w},
		{"after the escalation", def, 1, w},
		{"last flat repeat", def, 3, w},
		{"first doubling", def, 4, 2 * w},
		{"second", def, 5, 4 * w},
		{"third", def, 6, 8 * w},
		{"fourth", def, 7, 16 * w},
		{"fifth, 13h20m", def, 8, 32 * w},
		{"held at the ceiling", def, 9, 24 * time.Hour},
		{"far past it", def, 500, 24 * time.Hour},
		{"a window longer than the ceiling is not shortened", &ShiftPolicy{HandoffWindow: 48 * time.Hour}, 500, 48 * time.Hour},
		{"a nil policy uses the defaults", nil, 500, 24 * time.Hour},
		{"flat 1", &ShiftPolicy{HandoffWindow: w, HandoffRepeatFlat: 1}, 2, 2 * w},
		{"ceiling 1h", &ShiftPolicy{HandoffWindow: w, HandoffRepeatCeiling: time.Hour}, 9, time.Hour},
	} {
		if got := tc.p.HandoffRepeatGap(tc.delivered); got != tc.want {
			t.Errorf("%s: HandoffRepeatGap(%d) = %s, want %s", tc.name, tc.delivered, got, tc.want)
		}
	}
}
