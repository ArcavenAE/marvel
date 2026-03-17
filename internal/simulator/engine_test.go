package simulator

import (
	"testing"
)

func TestTickGrowth(t *testing.T) {
	t.Parallel()
	e := NewEngine("test", 42)

	// Tick should increase context percent
	e.TickOnce()
	if e.ContextPercent <= 0 {
		t.Fatalf("expected positive context after tick, got %.1f", e.ContextPercent)
	}
	if e.Tick != 1 {
		t.Fatalf("expected tick 1, got %d", e.Tick)
	}
	first := e.ContextPercent

	e.TickOnce()
	if e.ContextPercent <= first && !e.compressing {
		t.Fatalf("expected growth: %.1f should be > %.1f", e.ContextPercent, first)
	}
	if e.Tick != 2 {
		t.Fatalf("expected tick 2, got %d", e.Tick)
	}
}

func TestGrowthRate(t *testing.T) {
	t.Parallel()
	e := NewEngine("test", 99)

	// After 10 ticks without compression, growth should be modest (3-25%)
	// Use a seed that doesn't trigger early compression
	for i := 0; i < 10; i++ {
		e.TickOnce()
		if e.compressing {
			break
		}
	}

	// Context should be well under 50% after 10 ticks (max ~25% at 2.5/tick)
	if e.ContextPercent > 30 && !e.compressing {
		t.Fatalf("growth too fast: %.1f%% after 10 ticks", e.ContextPercent)
	}
}

func TestCompressionTriggersAndDrops(t *testing.T) {
	t.Parallel()
	e := NewEngine("test", 42)

	// Run until we see a compression event
	compressed := false
	for i := 0; i < 500; i++ {
		before := e.ContextPercent
		e.TickOnce()

		// Detect the drop after compression completes
		if e.ContextPercent < before-10 && before > 50 {
			compressed = true
			// After compression, should be in 3-16% range
			if e.ContextPercent < 3 || e.ContextPercent > 16 {
				t.Fatalf("post-compression context should be 3-16%%, got %.1f%%", e.ContextPercent)
			}
			break
		}
	}
	if !compressed {
		t.Fatal("expected compression event within 500 ticks")
	}
}

func TestCompressionPause(t *testing.T) {
	t.Parallel()
	e := NewEngine("test", 42)

	// Push context to trigger compression
	e.ContextPercent = 80
	e.TickOnce() // Should trigger compression

	if !e.compressing {
		// Threshold is random 75-89, might not trigger at exactly 80
		// Push higher
		e.ContextPercent = 90
		e.TickOnce()
	}

	if e.compressing {
		// During compression, context should be frozen
		frozen := e.ContextPercent
		e.TickOnce()
		if e.ContextPercent != frozen && e.compressing {
			t.Fatalf("context should be frozen during compression: was %.1f, now %.1f",
				frozen, e.ContextPercent)
		}
	}
}

func TestContextNeverDecreasesNormally(t *testing.T) {
	t.Parallel()
	e := NewEngine("test", 77)

	prev := 0.0
	for i := 0; i < 200; i++ {
		e.TickOnce()
		if e.ContextPercent < prev && !e.compressing && prev < 75 {
			// Context should only decrease during/after compression
			t.Fatalf("context decreased without compression at tick %d: %.1f -> %.1f",
				i+1, prev, e.ContextPercent)
		}
		if !e.compressing {
			prev = e.ContextPercent
		} else {
			prev = 0 // Reset tracking after compression
		}
	}
}

func TestStatusLine(t *testing.T) {
	t.Parallel()
	e := NewEngine("test-agent", 42)
	e.TickOnce()

	line := e.StatusLine()
	if len(line) == 0 {
		t.Fatal("expected non-empty status line")
	}

	if !containsStr(line, "test-agent") {
		t.Fatalf("status line should contain agent name: %s", line)
	}

	if !containsStr(line, "context:") {
		t.Fatalf("status line should contain context: %s", line)
	}
}

func TestStatusLineDuringCompression(t *testing.T) {
	t.Parallel()
	e := NewEngine("test", 42)
	e.compressing = true
	e.compressTicks = 3

	line := e.StatusLine()
	if !containsStr(line, "[compressing]") {
		t.Fatalf("status line should show compressing state: %s", line)
	}
}

func TestDeterministicWithSeed(t *testing.T) {
	t.Parallel()
	e1 := NewEngine("a", 123)
	e2 := NewEngine("b", 123)

	for i := 0; i < 20; i++ {
		e1.TickOnce()
		e2.TickOnce()
	}

	if e1.ContextPercent != e2.ContextPercent {
		t.Fatalf("same seed should produce same context: %.1f vs %.1f",
			e1.ContextPercent, e2.ContextPercent)
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
