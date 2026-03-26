// Command simulator is a Claude Code simulator for testing marvel orchestration.
// It simulates context window pressure with configurable tick rate and OTEL export.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/arcavenae/marvel/internal/daemon"
	marvelotel "github.com/arcavenae/marvel/internal/otel"
	"github.com/arcavenae/marvel/internal/simulator"
)

func main() {
	name := flag.String("name", "simulator", "agent name")
	socket := flag.String("socket", "", "marvel daemon socket path")
	scriptPath := flag.String("script", "", "Lua script path")
	otelStdout := flag.Bool("otel-stdout", false, "enable OTEL stdout export")
	tickMs := flag.Int("tick", 3000, "tick interval in milliseconds")
	workspace := flag.String("workspace", "default", "workspace name")
	team := flag.String("team", "default", "team name")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Printf("[agent %s] shutting down\n", *name)
		cancel()
	}()

	seed := uint64(time.Now().UTC().UnixNano())
	engine := simulator.NewEngine(*name, seed)

	// OTEL setup.
	if *otelStdout {
		provider, err := marvelotel.NewStdoutMeterProvider()
		if err != nil {
			log.Fatalf("otel provider: %v", err)
		}
		defer func() { _ = marvelotel.Shutdown(ctx, provider) }()

		meter := provider.Meter("marvel.simulator")
		gauge, err := marvelotel.NewContextGauge(meter)
		if err != nil {
			log.Fatalf("otel gauge: %v", err)
		}

		attrs := metric.WithAttributes(
			attribute.String("workspace", *workspace),
			attribute.String("team", *team),
			attribute.String("session", *name),
		)
		engine.OnRecord = func(pct float64) {
			gauge.Record(ctx, pct, attrs)
		}
	}

	// Heartbeat to daemon.
	if *socket != "" {
		sessionKey := *workspace + "/" + *name
		engine.OnHeartbeat = func(pct float64) error {
			params, _ := json.Marshal(map[string]any{
				"session_key":     sessionKey,
				"context_percent": pct,
			})
			resp, err := daemon.SendRequest(*socket, daemon.Request{
				Method: "heartbeat",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			return nil
		}
	}

	// Lua scripting.
	var luaEnv *simulator.LuaEnv
	if *scriptPath != "" {
		luaEnv = simulator.NewLuaEnv(*socket, *workspace, *team)
		defer luaEnv.Close()

		if err := luaEnv.LoadScript(*scriptPath); err != nil {
			log.Fatalf("load script %s: %v", *scriptPath, err)
		}
		fmt.Printf("[agent %s] loaded script: %s\n", *name, *scriptPath)

		engine.OnTick = func(pct float64, tick int) {
			if err := luaEnv.CallOnTick(pct, tick); err != nil {
				fmt.Printf("[agent %s] lua error: %v\n", *name, err)
			}
		}
	}

	fmt.Printf("[agent %s] starting | workspace=%s team=%s tick=%dms\n",
		*name, *workspace, *team, *tickMs)

	engine.Run(ctx, time.Duration(*tickMs)*time.Millisecond)
}
