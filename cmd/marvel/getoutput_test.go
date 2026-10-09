package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

func outputTeam() api.Team {
	rt := api.Runtime{Name: "claude", Command: "claude", Env: map[string]string{"DB_PASSWORD": "hunter2", "REGION": "eu-west"}}
	return api.Team{Name: "squad", Workspace: "ws", Roles: []api.Role{{Name: "worker", Replicas: 2, Runtime: rt}}}
}

func outputSessions() []api.Session {
	mk := func(name string, gen int64, st api.SessionState) api.Session {
		return api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", Generation: gen, State: st}
	}
	return []api.Session{
		mk("squad-worker-g1-0", 1, api.SessionRunning),
		mk("squad-worker-g1-1", 1, api.SessionRunning),
		mk("squad-worker-g0-0", 0, api.SessionFailed),
		{Name: "other-worker-g1-0", Workspace: "ws", Team: "other", Role: "worker", Generation: 1, State: api.SessionRunning},
	}
}

func teamDoc(t *testing.T) map[string]any {
	t.Helper()
	doc, err := buildGetOutput("team", []api.Team{outputTeam()}, nil, outputSessions())
	if err != nil {
		t.Fatalf("buildGetOutput: %v", err)
	}
	return doc
}

func section(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	s, ok := doc[name].(map[string]any)
	if !ok {
		t.Fatalf("the document has no %q section: %v", name, doc)
	}
	return s
}

// The output has two sections and each says what it is. The desired section is
// the stored record, not the applied file, with no round trip promised; the
// observed section is live state and says it is not for re-apply
// (aae-orc-zv4jw, operator ruling Q2 (c) of 2026-10-08).
func TestGetOutputHasLabelledDesiredAndObservedSections(t *testing.T) {
	doc := teamDoc(t)
	desired, observed := section(t, doc, "desired"), section(t, doc, "observed")
	if note, _ := desired["note"].(string); !strings.Contains(note, "not the applied file") || !strings.Contains(note, "no round trip") {
		t.Errorf("desired note = %q, want it to say it is not the applied file and promises no round trip", note)
	}
	if note, _ := observed["note"].(string); !strings.Contains(note, "not for re-apply") {
		t.Errorf("observed note = %q, want it to say it is not for re-apply", note)
	}
	if _, ok := desired["items"]; !ok {
		t.Errorf("desired has no items: %v", desired)
	}
	if _, ok := observed["items"]; !ok {
		t.Errorf("observed has no items: %v", observed)
	}
}

// A secret-looking Env value never reaches the output, even when the daemon
// sent it unredacted (an older daemon), and an ordinary value stays.
func TestGetOutputRedactsSecretEnvInBothFormats(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		out, err := renderGetOutput(teamDoc(t), format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if strings.Contains(out, "hunter2") {
			t.Errorf("%s output carries the secret value:\n%s", format, out)
		}
		if !strings.Contains(out, api.Redacted) || !strings.Contains(out, "eu-west") {
			t.Errorf("%s output = %q, want %q for the secret and eu-west kept", format, out, api.Redacted)
		}
	}
}

// The observed section counts this team's sessions by state per role and
// leaves another team's sessions out.
func TestGetOutputObservedCountsThisTeamsSessionsByState(t *testing.T) {
	out, err := renderGetOutput(teamDoc(t), "json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Observed struct {
			Items []struct {
				Team  string `json:"team"`
				Roles []struct {
					Role    string         `json:"role"`
					ByState map[string]int `json:"by_state"`
				} `json:"roles"`
			} `json:"items"`
		} `json:"observed"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(got.Observed.Items) != 1 || got.Observed.Items[0].Team != "squad" || len(got.Observed.Items[0].Roles) != 1 {
		t.Fatalf("observed items = %+v, want one team squad with one role", got.Observed.Items)
	}
	by := got.Observed.Items[0].Roles[0].ByState
	if by["running"] != 2 || by["failed"] != 1 || len(by) != 2 {
		t.Errorf("by_state = %v, want running 2 and failed 1 (the other team's session is not counted)", by)
	}
}

func TestGetOutputYAMLCarriesTheSameSections(t *testing.T) {
	out, err := renderGetOutput(teamDoc(t), "yaml")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode yaml: %v\n%s", err, out)
	}
	if _, ok := got["desired"].(map[string]any); !ok {
		t.Errorf("yaml has no desired section:\n%s", out)
	}
	if _, ok := got["observed"].(map[string]any); !ok {
		t.Errorf("yaml has no observed section:\n%s", out)
	}
}

func TestGetOutputWorkspaceCountsTeamsAndSessions(t *testing.T) {
	doc, err := buildGetOutput("workspace", []api.Team{outputTeam()}, []api.Workspace{{Name: "ws"}, {Name: "empty"}}, outputSessions())
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderGetOutput(doc, "json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Observed struct {
			Items []struct {
				Workspace string         `json:"workspace"`
				Teams     int            `json:"teams"`
				ByState   map[string]int `json:"by_state"`
			} `json:"items"`
		} `json:"observed"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	byName := map[string]int{}
	for i, it := range got.Observed.Items {
		byName[it.Workspace] = i
	}
	ws, ok := byName["ws"]
	if !ok || got.Observed.Items[ws].Teams != 1 || got.Observed.Items[ws].ByState["running"] != 3 || got.Observed.Items[ws].ByState["failed"] != 1 {
		t.Errorf("observed items = %+v, want ws with 1 team, running 3 and failed 1", got.Observed.Items)
	}
	if e, ok := byName["empty"]; !ok || got.Observed.Items[e].Teams != 0 || len(got.Observed.Items[e].ByState) != 0 {
		t.Errorf("observed items = %+v, want an empty workspace listed with no teams or sessions", got.Observed.Items)
	}
}

// cannedDaemon answers the get method with a fixed body per resource type.
func cannedDaemon(t *testing.T, bodies map[string]any) {
	t.Helper()
	home, err := os.MkdirTemp("", "mg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	oldSocket, oldCluster, oldGiven := socketPath, clusterName, clusterFlagGiven
	socketPath, clusterName, clusterFlagGiven = "", "", false
	t.Cleanup(func() { socketPath, clusterName, clusterFlagGiven = oldSocket, oldCluster, oldGiven })
	sock := filepath.Join(home, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			resp := daemon.Response{}
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				var p struct {
					ResourceType string `json:"resource_type"`
				}
				_ = json.Unmarshal(req.Params, &p)
				if body, ok := bodies[strings.TrimSuffix(p.ResourceType, "s")]; ok {
					resp.Result, _ = json.Marshal(body)
				}
			}
			_ = json.NewEncoder(c).Encode(resp)
			_ = c.Close()
		}
	}()
	t.Setenv(config.SocketEnv, sock)
}

func runGet(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := getCmd()
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var err error
	out := captureStdout(t, func() { err = cmd.Execute() })
	return out, err
}

func TestGetTeamOutputEndToEnd(t *testing.T) {
	cannedDaemon(t, map[string]any{"team": []api.Team{outputTeam()}, "session": outputSessions()})
	out, err := runGet(t, "team", "-o", "json")
	if err != nil {
		t.Fatalf("get team -o json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if _, ok := doc["desired"]; !ok {
		t.Errorf("output has no desired section:\n%s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("output carries the secret value:\n%s", out)
	}
}

func TestGetOutputIsForTeamsAndWorkspacesAndTwoFormats(t *testing.T) {
	cannedDaemon(t, map[string]any{"team": []api.Team{outputTeam()}, "session": outputSessions()})
	if _, err := runGet(t, "sessions", "-o", "json"); err == nil || !strings.Contains(err.Error(), "team and workspace") {
		t.Errorf("get sessions -o json error = %v, want one saying -o applies to team and workspace", err)
	}
	if _, err := runGet(t, "team", "-o", "xml"); err == nil || !strings.Contains(err.Error(), "json or yaml") {
		t.Errorf("get team -o xml error = %v, want one naming json or yaml", err)
	}
}

// -o with an empty value is a mistake, not a request for the table: the flag
// was given, so it is refused with the same words as any other bad format.
func TestGetOutputRefusesAnEmptyFormat(t *testing.T) {
	cannedDaemon(t, map[string]any{"team": []api.Team{outputTeam()}, "session": outputSessions()})
	out, err := runGet(t, "team", "-o", "")
	if err == nil || !strings.Contains(err.Error(), "json or yaml") {
		t.Errorf("get team -o \"\" error = %v, output %q, want an error naming json or yaml", err, out)
	}
}

// -o and --watch are two different views; the combination is refused rather
// than letting one silently win.
func TestGetOutputIsRefusedWithWatch(t *testing.T) {
	cannedDaemon(t, map[string]any{"team": []api.Team{outputTeam()}, "session": outputSessions()})
	if _, err := runGet(t, "team", "-o", "json", "--watch"); err == nil || !strings.Contains(err.Error(), "--watch") {
		t.Errorf("get team -o json --watch error = %v, want one naming --watch", err)
	}
}

// yaml is not json: json is valid yaml, so decoding alone cannot tell them
// apart. The yaml document has no braces at its top level and a key per line.
func TestGetOutputYAMLIsNotJSON(t *testing.T) {
	out, err := renderGetOutput(teamDoc(t), "yaml")
	if err != nil {
		t.Fatal(err)
	}
	var asJSON map[string]any
	if json.Unmarshal([]byte(out), &asJSON) == nil {
		t.Errorf("-o yaml printed json:\n%s", out)
	}
	for _, want := range []string{"\ndesired:\n", "\nobserved:\n", "kind: team\n"} {
		if !strings.Contains("\n"+out, want) {
			t.Errorf("yaml output lacks the line %q:\n%s", strings.TrimSpace(want), out)
		}
	}
}
