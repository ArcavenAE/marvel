package main

import (
	"strings"
	"testing"
)

// --ring rides on the step that carries the text, so a --clear step before it
// and a --verify step after it do not ring a second time.
func TestWithRingMarksOnlyTheTextStep(t *testing.T) {
	steps, err := resolveInject("wake up", true, nil, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	steps = applyInjectOptions(steps, true, true, 0)
	steps, err = withRing(steps, true, true)
	if err != nil {
		t.Fatalf("withRing: %v", err)
	}
	rung := 0
	for _, s := range steps {
		if s.Ring {
			rung++
			if s.Text != "wake up" || !s.Literal {
				t.Errorf("ring rides on %+v, want the text step", s)
			}
		}
	}
	if rung != 1 {
		t.Fatalf("%d steps carry the ring, want exactly 1: %+v", rung, steps)
	}
}

func TestWithRingOffChangesNothing(t *testing.T) {
	steps, _ := resolveInject("x", true, nil, true, false, false)
	out, err := withRing(steps, false, true)
	if err != nil || len(out) != 1 || out[0].Ring {
		t.Fatalf("withRing(false) = %+v, %v, want the steps untouched", out, err)
	}
}

// Keys only plus --ring is nothing to ring, refused before any RPC.
func TestWithRingRefusesKeysOnly(t *testing.T) {
	steps, err := resolveInject("", false, []string{"Enter"}, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = withRing(steps, true, false)
	if err == nil || !strings.Contains(err.Error(), "nothing to ring") {
		t.Fatalf("err = %v, want a refusal saying there is nothing to ring", err)
	}
}

func TestInjectRequestParamsCarryRingOnlyWhenSet(t *testing.T) {
	if _, ok := injectRequestParams("ws/s", injectStep{Text: "x", Literal: true})["ring"]; ok {
		t.Error("a step without Ring sent a ring param")
	}
	if got := injectRequestParams("ws/s", injectStep{Text: "x", Literal: true, Ring: true})["ring"]; got != true {
		t.Errorf("ring param = %v, want true", got)
	}
}

// With text followed by a --key step there are two requests; only the text
// step rings, or the key step would meet the claim the text step just wrote.
func TestWithRingLeavesTheKeyStepsAlone(t *testing.T) {
	steps, err := resolveInject("wake up", true, []string{"Enter"}, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("resolveInject made %d steps, want text then key", len(steps))
	}
	steps, err = withRing(steps, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !steps[0].Ring || steps[1].Ring {
		t.Fatalf("ring flags = %v, %v, want true on the text step and false on the key step", steps[0].Ring, steps[1].Ring)
	}
}
