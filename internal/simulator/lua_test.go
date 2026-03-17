package simulator

import (
	"os"
	"path/filepath"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestLuaEnvCreation(t *testing.T) {
	t.Parallel()
	env := NewLuaEnv("", "test-ws", "test-team")
	defer env.Close()

	// Verify marvel module is registered by calling marvel.log
	err := env.state.DoString(`marvel.log("lua env test")`)
	if err != nil {
		t.Fatalf("execute lua: %v", err)
	}
}

func TestCallOnTickNoFunction(t *testing.T) {
	t.Parallel()
	env := NewLuaEnv("", "test-ws", "test-team")
	defer env.Close()

	// on_tick not defined — should not error
	if err := env.CallOnTick(42.5, 10); err != nil {
		t.Fatalf("call on_tick with no function: %v", err)
	}
}

func TestCallOnTickWithFunction(t *testing.T) {
	t.Parallel()
	env := NewLuaEnv("", "test-ws", "test-team")
	defer env.Close()

	// Define on_tick that stores values in globals
	err := env.state.DoString(`
		last_pct = 0
		last_tick = 0
		function on_tick(pct, tick)
			last_pct = pct
			last_tick = tick
		end
	`)
	if err != nil {
		t.Fatalf("define on_tick: %v", err)
	}

	if err := env.CallOnTick(75.5, 42); err != nil {
		t.Fatalf("call on_tick: %v", err)
	}

	// Verify values were set
	pct := env.state.GetGlobal("last_pct")
	if pct.String() != "75.5" {
		t.Fatalf("expected last_pct 75.5, got %s", pct.String())
	}
	tick := env.state.GetGlobal("last_tick")
	if tick.String() != "42" {
		t.Fatalf("expected last_tick 42, got %s", tick.String())
	}
}

func TestLoadScript(t *testing.T) {
	t.Parallel()
	env := NewLuaEnv("", "test-ws", "test-team")
	defer env.Close()

	// Write a temp script
	dir := t.TempDir()
	script := filepath.Join(dir, "test.lua")
	if err := os.WriteFile(script, []byte(`
		test_loaded = true
		function on_tick(pct, tick)
			marvel.log("tick " .. tick .. " at " .. pct .. "%")
		end
	`), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	if err := env.LoadScript(script); err != nil {
		t.Fatalf("load script: %v", err)
	}

	loaded := env.state.GetGlobal("test_loaded")
	if loaded != lua.LTrue {
		t.Fatal("expected test_loaded to be true")
	}

	if err := env.CallOnTick(33.0, 5); err != nil {
		t.Fatalf("call on_tick from loaded script: %v", err)
	}
}

func TestLuaRPCWithoutSocket(t *testing.T) {
	t.Parallel()
	env := NewLuaEnv("", "test-ws", "test-team")
	defer env.Close()

	// create_agent without socket should return nil + error
	err := env.state.DoString(`
		key, err = marvel.create_agent("sleep", "300")
		if key ~= nil then
			error("expected nil key without socket")
		end
		if err == nil then
			error("expected error without socket")
		end
	`)
	if err != nil {
		t.Fatalf("lua error: %v", err)
	}
}
