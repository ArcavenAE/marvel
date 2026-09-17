package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

func baseSpec() Spec {
	return Spec{
		Domain:   "kinu",
		Listen:   "127.0.0.1:4222",
		StoreDir: "/state/nats",
		Admin:    User{Name: AdminUser, Password: "adminpw"},
		Teams: []TeamUser{
			{Workspace: "aae-orc", Team: "ops", Password: "opspw", Supervisor: true},
			{Workspace: "aae-orc", Team: "fleet", Password: "fleetpw"},
		},
	}
}

func TestMonitorAddr(t *testing.T) {
	t.Parallel()
	got, err := MonitorAddr("0.0.0.0:4222")
	if err != nil || got != "127.0.0.1:8222" {
		t.Errorf("MonitorAddr = %q, %v; want loopback at listen+4000", got, err)
	}
	if _, err := MonitorAddr("127.0.0.1:65000"); err == nil {
		t.Error("port that cannot carry the offset was accepted")
	}
	if _, err := MonitorAddr("4222"); err == nil {
		t.Error("bare port was accepted")
	}
}

func TestRenderConfLeafBlockOnlyWithHubAndSeed(t *testing.T) {
	t.Parallel()
	s := baseSpec()
	conf, err := RenderConf(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"listen: 127.0.0.1:4222", "http: 127.0.0.1:8222", `store_dir: "/state/nats/store"`, "domain: kinu", `include "authorization.conf"`} {
		if !strings.Contains(conf, want) {
			t.Errorf("conf missing %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "leafnodes") {
		t.Error("local-only cluster rendered a leafnodes block")
	}

	s.HubURL = "nats-leaf://192.168.100.110:7442"
	conf, _ = RenderConf(s)
	if strings.Contains(conf, "leafnodes") {
		t.Error("hub with no seed rendered a leafnodes block; it must start local-only and say so")
	}

	s.LeafSeed = true
	conf, _ = RenderConf(s)
	if !strings.Contains(conf, `urls: ["nats-leaf://192.168.100.110:7442"], nkey: $DIRECTOR_LEAF_NKEY }`) {
		t.Errorf("leaf remote not rendered with an unquoted whole-value nkey variable:\n%s", conf)
	}
	if strings.Contains(conf, `"$DIRECTOR_LEAF_NKEY"`) {
		t.Error("nkey variable is quoted; nats-server would take it literally")
	}
}

func TestRenderConfRejectsBadDomain(t *testing.T) {
	t.Parallel()
	s := baseSpec()
	s.Domain = "my.cluster"
	if _, err := RenderConf(s); err == nil || !strings.Contains(err.Error(), `"."`) {
		t.Errorf("bad domain accepted or byte not named: %v", err)
	}
}

func TestRenderAuthConfinesTeamsAndGrantsSupervisorOnlyWithHub(t *testing.T) {
	t.Parallel()
	s := baseSpec()
	auth, err := RenderAuth(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`user: marvel_admin, password: "adminpw"`,
		`user: ops, password: "opspw"`,
		`"agent.aae-orc.ops.>"`, `"agent.aae-orc.broadcast"`, `"agent.audit"`, `"$JS.API.>"`, `"$KV.AGENT_STATE.>"`, `"_INBOX.>"`,
		`user: fleet, password: "fleetpw"`, `"agent.aae-orc.fleet.>"`,
	} {
		if !strings.Contains(auth, want) {
			t.Errorf("auth missing %q:\n%s", want, auth)
		}
	}
	if strings.Contains(auth, "global.") {
		t.Error("local-only cluster granted global subjects")
	}
	// fleet must not see ops's subtree and vice versa: each own-subject appears
	// exactly twice (publish and subscribe of its own user).
	if n := strings.Count(auth, `"agent.aae-orc.ops.>"`); n != 2 {
		t.Errorf("ops subtree appears %d times, want 2 (its own publish and subscribe only)", n)
	}

	s.HubURL = "nats-leaf://h:7442"
	auth, _ = RenderAuth(s)
	if !strings.Contains(auth, `"global.director.inbox"`) || !strings.Contains(auth, `"$JS.global.API.>"`) || !strings.Contains(auth, `"global.kinu.>"`) {
		t.Errorf("supervisor team lacks global grants with a hub:\n%s", auth)
	}
	if n := strings.Count(auth, "global."); n != 3 {
		t.Errorf("global subjects appear %d times, want 3 (supervisor team only)", n)
	}
}

func TestRenderAuthRefusesDuplicateTeamAcrossWorkspaces(t *testing.T) {
	t.Parallel()
	s := baseSpec()
	s.Teams = append(s.Teams, TeamUser{Workspace: "other", Team: "ops", Password: "x"})
	_, err := RenderAuth(s)
	if err == nil || !strings.Contains(err.Error(), `"ops"`) || !strings.Contains(err.Error(), `"other"`) {
		t.Errorf("duplicate team not refused naming both: %v", err)
	}
	s = baseSpec()
	s.Teams[0].Team = "ops.team"
	if _, err := RenderAuth(s); err == nil {
		t.Error("team name outside the token class was accepted")
	}
}

func TestWriteFilesModesAndNoRewriteWhenUnchanged(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	changed, err := WriteFiles(dir, "conf-a", "auth-a")
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	for _, name := range []string{ConfName, AuthName} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o, want 0600", name, st.Mode().Perm())
		}
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 0700", st.Mode().Perm())
	}
	changed, err = WriteFiles(dir, "conf-a", "auth-a")
	if err != nil || changed {
		t.Errorf("unchanged content reported changed=%v err=%v", changed, err)
	}
	changed, _ = WriteFiles(dir, "conf-a", "auth-b")
	if !changed {
		t.Error("changed auth not reported")
	}
}

type teams []api.Team

func (t teams) ListTeams() []api.Team { return t }

type countingReloader struct{ n int }

func (c *countingReloader) Reload() error { c.n++; return nil }

func TestManagerRegenerateKeepsPasswordsDropsRemovedAndReloadsOnChange(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", StoreDir: dir, HubURL: "nats-leaf://h:7442"}
	live := &teams{
		{Name: "ops", Workspace: "aae-orc", Roles: []api.Role{{Name: "supervisor"}}},
		{Name: "fleet", Workspace: "aae-orc"},
	}
	seed := false
	m, err := NewManager(dir, "kinu", rb, live, func() bool { return seed })
	if err != nil {
		t.Fatal(err)
	}
	rl := &countingReloader{}
	m.Reloader = rl

	if changed, err := m.Regenerate(); err != nil || !changed {
		t.Fatalf("first regenerate: changed=%v err=%v", changed, err)
	}
	if rl.n != 1 {
		t.Errorf("reload count after first render = %d, want 1", rl.n)
	}
	opsPw, ok := m.TeamPassword("ops")
	if !ok || opsPw == "" {
		t.Fatal("ops has no minted password")
	}
	auth, _ := os.ReadFile(filepath.Join(dir, AuthName))
	if !strings.Contains(string(auth), opsPw) {
		t.Error("minted password not in the auth file")
	}
	if conf, _ := os.ReadFile(filepath.Join(dir, ConfName)); strings.Contains(string(conf), "leafnodes") {
		t.Error("leaf block rendered with no seed")
	}

	// No change: no rewrite, no reload.
	if changed, _ := m.Regenerate(); changed || rl.n != 1 {
		t.Errorf("no-op regenerate changed=%v reloads=%d", changed, rl.n)
	}

	// The seed appears: the leaf block renders, ops keeps its password.
	seed = true
	if changed, _ := m.Regenerate(); !changed {
		t.Error("seed appearing did not change the conf")
	}
	if conf, _ := os.ReadFile(filepath.Join(dir, ConfName)); !strings.Contains(string(conf), "nkey: $DIRECTOR_LEAF_NKEY") {
		t.Error("leaf block missing after the seed appeared")
	}
	if pw, _ := m.TeamPassword("ops"); pw != opsPw {
		t.Error("ops password changed across regenerations; a running session's credential would break")
	}

	// fleet is removed: its user and password go, which is revocation.
	*live = (*live)[:1]
	if changed, _ := m.Regenerate(); !changed {
		t.Error("removing a team did not change the auth file")
	}
	if _, ok := m.TeamPassword("fleet"); ok {
		t.Error("removed team still has a password in memory")
	}
	auth, _ = os.ReadFile(filepath.Join(dir, AuthName))
	if strings.Contains(string(auth), "user: fleet") {
		t.Error("removed team still in the auth file")
	}
	if rl.n != 3 {
		t.Errorf("reload count = %d, want 3 (one per change)", rl.n)
	}
}

// TestRenderedConfPassesNatsServerCheck feeds the rendered files to a real
// nats-server -t when one is on PATH: the renderer's contract is with that
// parser, not with this package's idea of it. Skipped without the binary.
func TestRenderedConfPassesNatsServerCheck(t *testing.T) {
	t.Parallel()
	ns, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	dir := filepath.Join(t.TempDir(), "nats")
	s := baseSpec()
	s.StoreDir = dir
	s.HubURL = "nats-leaf://192.168.100.110:7442"
	s.LeafSeed = true
	conf, err := RenderConf(s)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := RenderAuth(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFiles(dir, conf, auth); err != nil {
		t.Fatal(err)
	}
	confPath := filepath.Join(dir, ConfName)

	// With a real-shaped user seed in the variable, the config checks clean.
	// nats-server validates the seed's encoding at parse time, so a fake
	// string would fail on the value rather than prove the conf.
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ns, "-t", "-c", confPath)
	cmd.Env = append(os.Environ(), LeafSeedEnv+"="+string(seed))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nats-server -t rejected the rendered conf: %v\n%s\n--- conf ---\n%s\n--- auth ---\n%s", err, out, conf, auth)
	}
	// Without it, the server refuses loudly, which is the safe direction.
	cmd = exec.Command(ns, "-t", "-c", confPath)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("nats-server -t accepted the conf with %s unset; it should refuse:\n%s", LeafSeedEnv, out)
	}
}

func TestAdoptedGivesURLAndNoCredential(t *testing.T) {
	t.Parallel()
	a := NewAdopted(config.ResolvedBus{Mode: "adopted", Class: "message-bus", Provider: "nats-server", URL: "nats://127.0.0.1:4222"})
	if a.URL() != "nats://127.0.0.1:4222" {
		t.Errorf("URL = %q", a.URL())
	}
	if a.Bus().Mode != "adopted" {
		t.Errorf("adopted provider lost its record: %+v", a.Bus())
	}
	if _, _, ok := a.TeamCredential("ops"); ok {
		t.Error("adopted broker handed out a credential")
	}
}

func TestManagerTeamCredentialIsTeamNameAndMintedPassword(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", URL: "nats://127.0.0.1:4222", StoreDir: dir}
	m, err := NewManager(dir, "kinu", rb, teams{{Name: "ops", Workspace: "acme"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.URL() != "nats://127.0.0.1:4222" {
		t.Errorf("URL = %q", m.URL())
	}
	if _, _, ok := m.TeamCredential("ops"); ok {
		t.Error("credential available before the team was rendered")
	}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	user, pw, ok := m.TeamCredential("ops")
	if !ok || user != "ops" || pw == "" {
		t.Fatalf("TeamCredential(ops) = %q %q %v", user, pw, ok)
	}
	if got, _ := m.TeamPassword("ops"); got != pw {
		t.Error("TeamCredential password differs from TeamPassword")
	}
	if _, _, ok := m.TeamCredential("nobody"); ok {
		t.Error("credential for an unapplied team")
	}
}
