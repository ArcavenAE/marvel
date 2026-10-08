package bus

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// Stage 2 of per-role broker users (docs/design/per-role-broker-users.md,
// M9-3, additive). A team that declares a supervisor and has a hub gets a
// second user, `<team>.supervisor`, beside `<team>`. The team user keeps every
// grant it has today so a running supervisor loses nothing at reload; the
// narrowing and the removal of the team user's global grants are Stage 3.

func hubSpec() Spec {
	s := baseSpec()
	s.HubURL = "tls://hub.example:7422"
	return s
}

func principalNamed(t *testing.T, ps []Principal, name string) Principal {
	t.Helper()
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no principal %q in %v", name, principalNames(ps))
	return Principal{}
}

func principalNames(ps []Principal) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

// Test 1 (supervisor half): the dotted user carries exactly the section 3
// grants: the team's own subtree and plumbing, the three global publishes, and
// a subscribe narrowed to the cluster's supervisor inbox, not global.<domain>.>.
func TestSupervisorUserCarriesExactlyTheDeclaredGrants(t *testing.T) {
	t.Parallel()
	ps, err := DeclaredPrincipals(hubSpec())
	if err != nil {
		t.Fatal(err)
	}
	sup := principalNamed(t, ps, "ops.supervisor")
	if sup.Scope != ScopeBinding || sup.Team != "ops" || sup.Password != "suppw" {
		t.Errorf("principal = %+v, want a binding-scope user of team ops with the supervisor password", sup)
	}
	wantPub := append([]string{"agent.aae-orc.ops.>", "agent.audit"}, plumbingPublish...)
	wantPub = append(wantPub, "global.director.inbox", "global.*.supervisor.inbox", "$JS.global.API.>")
	if !slices.Equal(sup.Publish, wantPub) {
		t.Errorf("publish = %v\nwant      %v", sup.Publish, wantPub)
	}
	wantSub := append([]string{"agent.aae-orc.ops.>", "agent.aae-orc.broadcast"}, plumbingSubscribe...)
	wantSub = append(wantSub, "global.kinu.supervisor.inbox")
	if !slices.Equal(sup.Subscribe, wantSub) {
		t.Errorf("subscribe = %v\nwant       %v", sup.Subscribe, wantSub)
	}
	if contains(sup.Subscribe, "global.kinu.>") {
		t.Error("the supervisor user subscribes the whole global domain; it must be narrowed to its inbox")
	}
}

// Additive: the team user is exactly what it was, global grants included, so a
// running supervisor connected as `<team>` is untouched by this change.
func TestTeamUserKeepsItsGlobalGrantsInStage2(t *testing.T) {
	t.Parallel()
	ps, err := DeclaredPrincipals(hubSpec())
	if err != nil {
		t.Fatal(err)
	}
	team := principalNamed(t, ps, "ops")
	for _, want := range []string{"global.director.inbox", "global.*.supervisor.inbox", "$JS.global.API.>"} {
		if !contains(team.Publish, want) {
			t.Errorf("team user lost publish %q: %v", want, team.Publish)
		}
	}
	if !contains(team.Subscribe, "global.kinu.>") {
		t.Errorf("team user lost its global subscribe: %v", team.Subscribe)
	}
}

// No supervisor user without a hub, and none for a team with no supervisor.
func TestNoSupervisorUserWithoutAHubOrASupervisor(t *testing.T) {
	t.Parallel()
	noHub := hubSpec()
	noHub.HubURL = ""
	ps, err := DeclaredPrincipals(noHub)
	if err != nil {
		t.Fatal(err)
	}
	if got := principalNames(ps); slices.Contains(got, "ops.supervisor") {
		t.Errorf("a supervisor user was rendered with no hub: %v", got)
	}
	ps, err = DeclaredPrincipals(hubSpec())
	if err != nil {
		t.Fatal(err)
	}
	if got := principalNames(ps); slices.Contains(got, "fleet.supervisor") {
		t.Errorf("a team with no supervisor got a supervisor user: %v", got)
	}
}

// A supervisor user with no password is refused, never rendered open.
func TestSupervisorUserWithoutAPasswordIsRefused(t *testing.T) {
	t.Parallel()
	s := hubSpec()
	for i := range s.Teams {
		for j := range s.Teams[i].GlobalRoles {
			s.Teams[i].GlobalRoles[j].Password = ""
		}
	}
	if _, err := DeclaredPrincipals(s); err == nil || !strings.Contains(err.Error(), "supervisor") {
		t.Fatalf("error = %v, want a refusal that names the supervisor user", err)
	}
}

func hubManager(t *testing.T, dir string, live *teams) *Manager {
	t.Helper()
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", URL: "nats://127.0.0.1:4222", StoreDir: dir, HubURL: "tls://hub.example:7422"}
	m, err := NewManager(dir, "kinu", rb, live, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// acceptAll stands in for a broker that has loaded the rendered file.
func acceptAll(context.Context, string, string, string) error { return nil }

// confirmed marks every rendered per-role user as accepted by the broker.
func confirmed(t *testing.T, m *Manager) {
	t.Helper()
	m.verify = acceptAll
	if err := m.ConfirmRoleUsers(context.Background()); err != nil {
		t.Fatalf("confirm: %v", err)
	}
}

func supervisedTeam() api.Team {
	return api.Team{Name: "ops", Workspace: "acme", Roles: []api.Role{{Name: "supervisor"}, {Name: "worker"}}}
}

// Test 2: credential selection by role.
func TestCredentialSelectsTheSupervisorUserByRole(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	m := hubManager(t, dir, &teams{supervisedTeam()})
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	confirmed(t, m)
	su, spw, ok := m.Credential("ops", "supervisor")
	if !ok || su != "ops.supervisor" || spw == "" {
		t.Fatalf("Credential(ops, supervisor) = %q %q %v, want the dotted user", su, spw, ok)
	}
	wu, wpw, ok := m.Credential("ops", "worker")
	if !ok || wu != "ops" || wpw == "" {
		t.Fatalf("Credential(ops, worker) = %q %q %v, want the team user", wu, wpw, ok)
	}
	if spw == wpw {
		t.Error("the supervisor and team users share a password")
	}
	if _, _, ok := m.Credential("nobody", "supervisor"); ok {
		t.Error("a credential for an unapplied team")
	}
}

// With no hub the dotted user does not exist, so the supervisor role falls
// back to the team user rather than failing to connect.
func TestSupervisorRoleFallsBackToTheTeamUserWithoutAHub(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", URL: "nats://127.0.0.1:4222", StoreDir: dir}
	m, err := NewManager(dir, "kinu", rb, &teams{supervisedTeam()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if u, _, ok := m.Credential("ops", "supervisor"); !ok || u != "ops" {
		t.Fatalf("Credential(ops, supervisor) with no hub = %q %v, want the team user", u, ok)
	}
}

// The dotted user's password survives a daemon restart (Stage 1's recovery) and
// the team user's does not change when the dotted user is added: neither
// credential a running seat holds is reminted.
func TestSupervisorUserPasswordSurvivesARestartAndLeavesTheTeamUserAlone(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	live := &teams{supervisedTeam()}
	m1 := hubManager(t, dir, live)
	if _, err := m1.Regenerate(); err != nil {
		t.Fatal(err)
	}
	confirmed(t, m1)
	_, sup1, _ := m1.Credential("ops", "supervisor")
	_, team1, _ := m1.Credential("ops", "worker")
	m2 := hubManager(t, dir, live)
	if _, err := m2.Regenerate(); err != nil {
		t.Fatal(err)
	}
	confirmed(t, m2)
	if _, sup2, _ := m2.Credential("ops", "supervisor"); sup2 != sup1 {
		t.Error("the supervisor user's password changed across a restart")
	}
	if _, team2, _ := m2.Credential("ops", "worker"); team2 != team1 {
		t.Error("the team user's password changed across a restart")
	}
}

// The first render after an upgrade: a store that only knew `ops` gains the
// dotted user, and `ops` keeps its password.
func TestUpgradeAddsTheSupervisorUserWithoutReminting(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", URL: "nats://127.0.0.1:4222", StoreDir: dir}
	old, err := NewManager(dir, "kinu", rb, &teams{supervisedTeam()}, nil) // no hub: the pre-upgrade shape
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Regenerate(); err != nil {
		t.Fatal(err)
	}
	_, before, _ := old.Credential("ops", "worker")
	m := hubManager(t, dir, &teams{supervisedTeam()}) // the hub arrives
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	confirmed(t, m)
	if _, after, _ := m.Credential("ops", "worker"); after != before {
		t.Error("the team user's password was reminted when the supervisor user was added")
	}
	if u, _, ok := m.Credential("ops", "supervisor"); !ok || u != "ops.supervisor" {
		t.Errorf("supervisor credential = %q %v, want the dotted user", u, ok)
	}
}

// The dotted user is pruned when the team stops declaring a supervisor or goes
// away, so a stale secret does not outlive its line.
func TestSupervisorUserIsPrunedWhenTheRoleGoes(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	live := &teams{supervisedTeam()}
	m := hubManager(t, dir, live)
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	confirmed(t, m)
	*live = teams{api.Team{Name: "ops", Workspace: "acme", Roles: []api.Role{{Name: "worker"}}}}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if u, _, ok := m.Credential("ops", "supervisor"); !ok || u != "ops" {
		t.Errorf("after the supervisor role went, Credential = %q %v, want the team user", u, ok)
	}
	if _, ok := RecoverPasswords(filepath.Join(dir, AuthName))["ops.supervisor"]; ok {
		t.Error("the supervisor user is still in authorization.conf")
	}
}

// Against a real broker: the dotted user name parses and authenticates, may
// publish another cluster's supervisor inbox, may subscribe its own inbox, and
// may not subscribe the rest of the global domain.
func TestSupervisorUserAgainstARealBroker(t *testing.T) {
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skip("nats-server not on PATH")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "state", "nats")
	listen := "127.0.0.1:" + strconv.Itoa(freePort(t))
	domain := "t" + strconv.Itoa(freePort(t))
	rb := config.ResolvedBus{Managed: true, Listen: listen, URL: "nats://" + listen, StoreDir: dir, HubURL: "tls://127.0.0.1:1"}
	m, err := NewManager(dir, domain, rb, &teams{supervisedTeam()}, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSupervisor(m, filepath.Join(root, "run"), filepath.Join(root, "log"), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.backoff = func(int) time.Duration { return 200 * time.Millisecond }
	s.dialTimeout = 5 * time.Second
	s.AfterReady = func() error {
		a := m.Admin()
		_, err := Provision(context.Background(), m.URL(), a.Name, a.Password)
		return err
	}
	t.Cleanup(func() { s.Stop(false) })
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	var user, pw string
	eventually(t, "the supervisor user to be handed out once the broker accepts it", func() bool {
		var ok bool
		user, pw, ok = m.Credential("ops", "supervisor")
		return ok && user == "ops.supervisor"
	})
	violations := make(chan string, 8)
	nc, err := nats.Connect(m.URL(), nats.UserInfo(user, pw), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
		violations <- err.Error()
	}))
	if err != nil {
		t.Fatalf("the dotted supervisor user does not authenticate: %v", err)
	}
	defer nc.Close()
	// Flush returns once the server has answered, but the client's error
	// callback runs on its own goroutine, so a violation can land after
	// Flush. A check that expects NO violation can only wait a short while
	// (a slow machine makes it pass, never fail). A check that expects one
	// has to wait up to a deadline, or a late callback fails it.
	// Each check names its own subject, so one check's late violation never
	// stands in for another's.
	violatedWithin := func(subject string, wait time.Duration) bool {
		t.Helper()
		_ = nc.Flush()
		return receivesViolation(violations, subject, wait)
	}
	noViolation := func(subject string) bool { t.Helper(); return !violatedWithin(subject, 500*time.Millisecond) }
	awaitViolation := func(subject string) bool { t.Helper(); return violatedWithin(subject, 5*time.Second) }
	if err := nc.Publish("global.other.supervisor.inbox", []byte("x")); err != nil || !noViolation("global.other.supervisor.inbox") {
		t.Errorf("publishing another cluster's supervisor inbox was refused: %v", err)
	}
	own := fmt.Sprintf("global.%s.supervisor.inbox", domain)
	if _, err := nc.SubscribeSync(own); err != nil || !noViolation(own) {
		t.Errorf("subscribing the supervisor's own inbox was refused: %v", err)
	}
	rest := fmt.Sprintf("global.%s.somebody-else", domain)
	if _, err := nc.SubscribeSync(rest); err != nil || !awaitViolation(rest) {
		t.Errorf("subscribing the rest of the global domain was allowed (err %v); the grant must be narrowed", err)
	}
	drainViolations(violations)
}

// receivesViolation reports whether, within wait, the client's callback
// delivers a permissions violation naming subject. It returns as soon as one
// arrives, so a long wait costs nothing when the callback is prompt. Errors
// that are not that violation (another subject, another kind) are skipped and
// the wait goes on, so a late violation from an earlier check cannot satisfy
// a later one.
func receivesViolation(errs <-chan string, subject string, wait time.Duration) bool {
	deadline := time.After(wait)
	for {
		select {
		case e := <-errs:
			if strings.Contains(e, "Permissions Violation") && strings.Contains(e, `"`+subject+`"`) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// drainViolations empties what the callback has delivered so far, so the
// channel holds nothing a later check could take for its own.
func drainViolations(errs <-chan string) {
	for {
		select {
		case <-errs:
		default:
			return
		}
	}
}

// The client's error callback runs on its own goroutine, so a violation the
// server has already sent can arrive after Flush returns. A check that
// expects one must wait to a deadline: a violation 300ms late is seen, a
// message that is not a violation is not one, and silence is false. This
// needs no broker, so it also runs where nats-server is not on PATH.
func TestReceivesViolationWaitsForALateCallback(t *testing.T) {
	t.Parallel()
	late := make(chan string, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		late <- `nats: Permissions Violation for Subscription to "x"`
	}()
	if !receivesViolation(late, "x", 5*time.Second) {
		t.Error("a violation 300ms late was missed inside a 5s wait")
	}

	other := make(chan string, 1)
	other <- "nats: slow consumer"
	if receivesViolation(other, "x", time.Second) {
		t.Error("a message that is not a permissions violation was taken for one")
	}

	if receivesViolation(make(chan string), "x", 50*time.Millisecond) {
		t.Error("silence was reported as a violation")
	}
}

// A violation for another subject is not this check's violation. An earlier
// check's violation that lands late must not satisfy a later check's wait,
// and must not hide the later check's own violation behind it.
func TestReceivesViolationMatchesTheSubject(t *testing.T) {
	t.Parallel()
	earlier := make(chan string, 2)
	earlier <- `nats: Permissions Violation for Publish to "global.other.supervisor.inbox"`
	if receivesViolation(earlier, "global.ops.somebody-else", 200*time.Millisecond) {
		t.Error("another subject's violation satisfied this subject's wait")
	}

	both := make(chan string, 2)
	both <- `nats: Permissions Violation for Publish to "global.other.supervisor.inbox"`
	both <- `nats: Permissions Violation for Subscription to "global.ops.somebody-else"`
	if !receivesViolation(both, "global.ops.somebody-else", time.Second) {
		t.Error("the right subject's violation was missed behind another's")
	}

	// The same subject in some other kind of error is not a violation.
	kind := make(chan string, 1)
	kind <- `nats: slow consumer on "global.ops.somebody-else"`
	if receivesViolation(kind, "global.ops.somebody-else", 200*time.Millisecond) {
		t.Error("a non-violation error naming the subject was taken for a violation")
	}

	// A subject that is only a prefix of the one named is a different subject.
	prefix := make(chan string, 1)
	prefix <- `nats: Permissions Violation for Subscription to "global.ops.somebody-else.extra"`
	if receivesViolation(prefix, "global.ops.somebody-else", 200*time.Millisecond) {
		t.Error("a longer subject was taken for the named one")
	}
}

func TestDrainViolationsEmptiesTheChannel(t *testing.T) {
	t.Parallel()
	errs := make(chan string, 4)
	errs <- "a"
	errs <- "b"
	drainViolations(errs)
	if len(errs) != 0 {
		t.Errorf("%d errors left after the drain", len(errs))
	}
	drainViolations(errs) // an empty channel must not block
}
