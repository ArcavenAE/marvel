package simulator

import (
	"encoding/json"
	"fmt"

	"github.com/arcavenae/marvel/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

// LuaEnv wraps a Lua state with marvel API bindings.
type LuaEnv struct {
	state      *lua.LState
	socketPath string
	workspace  string
	team       string
	role       string
}

// NewLuaEnv creates a Lua environment with the marvel module registered.
func NewLuaEnv(socketPath, workspace, team, role string) *LuaEnv {
	env := &LuaEnv{
		state:      lua.NewState(),
		socketPath: socketPath,
		workspace:  workspace,
		team:       team,
		role:       role,
	}
	env.registerModule()
	return env
}

func (e *LuaEnv) registerModule() {
	mod := e.state.NewTable()

	e.state.SetField(mod, "create_agent", e.state.NewFunction(e.luaCreateAgent))
	e.state.SetField(mod, "kill_agent", e.state.NewFunction(e.luaKillAgent))
	e.state.SetField(mod, "list_agents", e.state.NewFunction(e.luaListAgents))
	e.state.SetField(mod, "scale_team", e.state.NewFunction(e.luaScaleTeam))
	e.state.SetField(mod, "log", e.state.NewFunction(e.luaLog))

	e.state.SetGlobal("marvel", mod)
}

// LoadScript loads and executes a Lua script file.
func (e *LuaEnv) LoadScript(path string) error {
	return e.state.DoFile(path)
}

// CallOnTick calls the global on_tick(pct, tick) function if it exists.
func (e *LuaEnv) CallOnTick(pct float64, tick int) error {
	fn := e.state.GetGlobal("on_tick")
	if fn == lua.LNil {
		return nil
	}
	return e.state.CallByParam(lua.P{
		Fn:      fn,
		NRet:    0,
		Protect: true,
	}, lua.LNumber(pct), lua.LNumber(tick))
}

// Close shuts down the Lua state.
func (e *LuaEnv) Close() {
	e.state.Close()
}

func (e *LuaEnv) sendRPC(method string, params any) (string, error) {
	if e.socketPath == "" {
		return "", fmt.Errorf("no socket path configured")
	}
	data, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("marshal params: %w", err)
	}
	resp, err := daemon.SendRequest(e.socketPath, daemon.Request{
		Method: method,
		Params: data,
	})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("%s", resp.Error)
	}
	return string(resp.Result), nil
}

// marvel.create_agent(command, [args...]) -> session_key
func (e *LuaEnv) luaCreateAgent(L *lua.LState) int {
	command := L.CheckString(1)

	var args []string
	for i := 2; i <= L.GetTop(); i++ {
		args = append(args, L.CheckString(i))
	}

	result, err := e.sendRPC("run", map[string]any{
		"workspace":       e.workspace,
		"team":            e.team,
		"role":            "adhoc",
		"runtime_command": command,
		"runtime_args":    args,
	})
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	var parsed map[string]string
	_ = json.Unmarshal([]byte(result), &parsed)
	L.Push(lua.LString(parsed["session_key"]))
	return 1
}

// marvel.kill_agent(session_key) -> ok, err
func (e *LuaEnv) luaKillAgent(L *lua.LState) int {
	sessionKey := L.CheckString(1)
	_, err := e.sendRPC("delete", map[string]string{
		"resource_type": "session",
		"name":          sessionKey,
	})
	if err != nil {
		L.Push(lua.LFalse)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}

// marvel.list_agents() -> table of session keys
func (e *LuaEnv) luaListAgents(L *lua.LState) int {
	result, err := e.sendRPC("get", map[string]string{
		"resource_type": "sessions",
	})
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	var sessions []map[string]any
	_ = json.Unmarshal([]byte(result), &sessions)

	tbl := L.NewTable()
	for _, s := range sessions {
		ws, _ := s["Workspace"].(string)
		tm, _ := s["Team"].(string)
		name, _ := s["Name"].(string)
		if ws == e.workspace && tm == e.team && name != "" {
			tbl.Append(lua.LString(ws + "/" + name))
		}
	}
	L.Push(tbl)
	return 1
}

// marvel.scale_team(team_key, role, replicas) -> ok, err
func (e *LuaEnv) luaScaleTeam(L *lua.LState) int {
	teamKey := L.CheckString(1)
	role := L.CheckString(2)
	replicas := L.CheckInt(3)
	_, err := e.sendRPC("scale", map[string]any{
		"team_key": teamKey,
		"role":     role,
		"replicas": replicas,
	})
	if err != nil {
		L.Push(lua.LFalse)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}

// marvel.log(message)
func (e *LuaEnv) luaLog(L *lua.LState) int {
	msg := L.CheckString(1)
	fmt.Printf("[lua] %s\n", msg)
	return 0
}
