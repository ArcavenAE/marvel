// Package simulator implements a Claude Code simulator for load testing marvel.
package simulator

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"
)

// Engine simulates agent context window pressure with realistic behavior:
// - Context grows in variable jumps (0.3-2.5% per tick, modeling tool calls)
// - At 75-89%, a compression event triggers: pause then drop to 3-16%
// - Context never decreases except during compression
type Engine struct {
	Name           string
	ContextPercent float64
	Tick           int
	rng            *rand.Rand

	// Compression state
	compressing    bool
	compressTicks  int
	compressTarget float64

	// OnTick is called each tick with current context percent and tick count.
	OnTick func(pct float64, tick int)
	// OnHeartbeat sends heartbeat to daemon.
	OnHeartbeat func(pct float64) error
	// OnRecord records OTEL metric.
	OnRecord func(pct float64)
}

// NewEngine creates a simulator engine with the given name and random seed.
func NewEngine(name string, seed uint64) *Engine {
	return &Engine{
		Name: name,
		rng:  rand.New(rand.NewPCG(seed, seed)),
	}
}

// TickOnce advances the simulation by one step.
func (e *Engine) TickOnce() {
	e.Tick++

	if e.compressing {
		e.compressTicks--
		if e.compressTicks <= 0 {
			// Compression complete — drop to target
			e.compressing = false
			fmt.Printf("[agent %s] compression complete: %.0f%% -> %.0f%%\n",
				e.Name, e.ContextPercent, e.compressTarget)
			e.ContextPercent = e.compressTarget
		}
		// During compression, context stays frozen (the pause)
		return
	}

	// Normal growth: variable jumps modeling tool calls and responses.
	// Small ticks (thinking/typing): 0.3-0.8%
	// Medium ticks (tool call + result): 1.0-1.8%
	// Large ticks (big file read / long response): 1.5-2.5%
	roll := e.rng.Float64()
	var growth float64
	switch {
	case roll < 0.5:
		// Small tick — most common
		growth = 0.3 + e.rng.Float64()*0.5
	case roll < 0.85:
		// Medium tick
		growth = 1.0 + e.rng.Float64()*0.8
	default:
		// Large tick — occasional big reads
		growth = 1.5 + e.rng.Float64()*1.0
	}
	e.ContextPercent += growth

	// Check for compression trigger at 75-89%
	threshold := 75.0 + e.rng.Float64()*14.0
	if e.ContextPercent >= threshold {
		e.compressing = true
		// Pause for 2-5 ticks (simulating compression processing time)
		e.compressTicks = 2 + e.rng.IntN(4)
		// Drop target: 3-16%
		e.compressTarget = 3.0 + e.rng.Float64()*13.0
		fmt.Printf("[agent %s] context pressure %.0f%% >= %.0f%% — compressing (pause %d ticks, target %.0f%%)\n",
			e.Name, e.ContextPercent, threshold, e.compressTicks, e.compressTarget)
	}
}

// StatusLine returns the formatted status line for this tick.
func (e *Engine) StatusLine() string {
	state := ""
	if e.compressing {
		state = " [compressing]"
	}
	return fmt.Sprintf("[agent %s] context: %.1f%%%s | tick %d | %s",
		e.Name, e.ContextPercent, state, e.Tick, time.Now().UTC().Format(time.RFC3339))
}

// Run starts the main simulation loop with the given tick interval.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.TickOnce()
			fmt.Println(e.StatusLine())

			if e.OnRecord != nil {
				e.OnRecord(e.ContextPercent)
			}
			if e.OnHeartbeat != nil {
				if err := e.OnHeartbeat(e.ContextPercent); err != nil {
					fmt.Printf("[agent %s] heartbeat error: %v\n", e.Name, err)
				}
			}
			if e.OnTick != nil {
				e.OnTick(e.ContextPercent, e.Tick)
			}
		}
	}
}
