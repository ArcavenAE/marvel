package bus

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The three objects keep the exact parameters of precondition 6; adding
// one here without its grant, or the other way around, is what the set
// exists to prevent, so the shape is pinned.
func TestDeclaredObjectsKeepPrecondition6Parameters(t *testing.T) {
	t.Parallel()
	objs := DeclaredObjects()
	if len(objs) != 3 {
		t.Fatalf("declared %d objects, want 3", len(objs))
	}
	byName := map[string]Object{}
	for _, o := range objs {
		if o.Scope != ScopeService || o.Origin == "" {
			t.Errorf("%s: scope %q origin %q; every object is service scope with an origin", o.Name, o.Scope, o.Origin)
		}
		if (o.Kind == ObjectStream) != (o.Stream != nil) || (o.Kind == ObjectKV) != (o.KV != nil) {
			t.Errorf("%s: kind %q does not match its config", o.Name, o.Kind)
		}
		byName[o.Name] = o
	}
	inbox := byName[InboxStream].Stream
	if inbox == nil || len(inbox.Subjects) != 2 || inbox.MaxAge != 24*time.Hour || inbox.MaxMsgSize != 65536 || inbox.Duplicates != 2*time.Minute || inbox.Storage != jetstream.FileStorage || inbox.Retention != jetstream.LimitsPolicy {
		t.Errorf("AGENT_INBOX = %+v", inbox)
	}
	audit := byName[AuditStream].Stream
	if audit == nil || audit.MaxAge != 720*time.Hour || len(audit.Subjects) != 1 || audit.Subjects[0] != "agent.audit" {
		t.Errorf("AGENT_AUDIT = %+v", audit)
	}
	state := byName[StateBucket].KV
	if state == nil || state.TTL != 90*time.Second || state.Storage != jetstream.FileStorage {
		t.Errorf("AGENT_STATE = %+v", state)
	}
}

func TestDeclaredPrincipalsOrderScopesAndReservedNames(t *testing.T) {
	t.Parallel()
	s := baseSpec()
	s.Seat = &SeatUser{Workspace: "aae-orc", Team: "ops", Password: "seatpw"}
	ps, err := DeclaredPrincipals(s)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, p.Name)
	}
	if got := strings.Join(names, ","); got != "marvel_admin,director,fleet,ops" {
		t.Errorf("principal order = %s, want admin, seat, then teams sorted", got)
	}
	if ps[0].Scope != ScopeService || ps[1].Scope != ScopeService || ps[2].Scope != ScopeBinding || ps[2].Team != "fleet" {
		t.Errorf("scopes = %+v", ps)
	}
	seat := ps[1]
	for _, want := range []string{"agent.*.*.*.inbox", "agent.*.*.role.*.inbox", "agent.*.*.broadcast", "agent.*.broadcast", "agent.audit", "$JS.API.>", "_INBOX.>"} {
		if !contains(seat.Publish, want) {
			t.Errorf("seat publish lacks %q: %v", want, seat.Publish)
		}
	}
	for _, want := range []string{"agent.aae-orc.ops.>", "agent.aae-orc.broadcast", "$KV.AGENT_STATE.>", "_INBOX.>"} {
		if !contains(seat.Subscribe, want) {
			t.Errorf("seat subscribe lacks %q: %v", want, seat.Subscribe)
		}
	}
	for _, sub := range seat.Subscribe {
		if strings.HasPrefix(sub, "agent.*") {
			t.Errorf("seat subscribes across workspaces: %q", sub)
		}
	}

	// A team by a reserved user name is refused, never merged.
	for _, r := range config.ReservedBusUsers {
		bad := baseSpec()
		bad.Teams = append(bad.Teams, TeamUser{Workspace: "aae-orc", Team: r, Password: "x"})
		if _, err := DeclaredPrincipals(bad); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("team %q accepted: %v", r, err)
		}
	}
	// A seat outside the token class or without a password is refused.
	bad := baseSpec()
	bad.Seat = &SeatUser{Workspace: "aae-orc", Team: "ops.x", Password: "x"}
	if _, err := DeclaredPrincipals(bad); err == nil {
		t.Error("seat team outside the token class accepted")
	}
	bad.Seat = &SeatUser{Workspace: "aae-orc", Team: "ops"}
	if _, err := DeclaredPrincipals(bad); err == nil {
		t.Error("seat with no password accepted")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// The rendered auth file carries the director user between admin and the
// teams, with the seat's grants and none of the teams' subtrees.
func TestRenderAuthRendersTheSeatUser(t *testing.T) {
	t.Parallel()
	s := baseSpec()
	auth, err := RenderAuth(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auth, "user: director") {
		t.Error("director user rendered with no seat declared")
	}
	s.Seat = &SeatUser{Workspace: "aae-orc", Team: "ops", Password: "seatpw"}
	auth, err = RenderAuth(s)
	if err != nil {
		t.Fatal(err)
	}
	admin := strings.Index(auth, "user: marvel_admin")
	seat := strings.Index(auth, `user: director, password: "seatpw"`)
	ops := strings.Index(auth, "user: ops")
	if seat < 0 || admin >= seat || seat >= ops {
		t.Errorf("seat user not rendered between admin and the teams:\n%s", auth)
	}
	if !strings.Contains(auth, `"agent.*.*.*.inbox"`) || !strings.Contains(auth, `"agent.*.broadcast"`) {
		t.Errorf("seat grants missing:\n%s", auth)
	}
	// The seat reads its own subtree; ops's own-subject now appears three
	// times (ops publish, ops subscribe, seat subscribe), and fleet's still
	// twice.
	if n := strings.Count(auth, `"agent.aae-orc.ops.>"`); n != 3 {
		t.Errorf("ops subtree appears %d times, want 3", n)
	}
	if n := strings.Count(auth, `"agent.aae-orc.fleet.>"`); n != 2 {
		t.Errorf("fleet subtree appears %d times, want 2", n)
	}
}

// selfSignedCA writes a throwaway CA certificate and returns its path, so
// the rendered tls block points at something nats-server can parse.
func selfSignedCA(t *testing.T, dir string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "marvel test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// hub.ca_file renders into the leaf remote's tls block, and only there;
// a real nats-server -t accepts the result when the binary is on PATH.
func TestRenderConfLeafRemoteCarriesTheHubCAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ca := selfSignedCA(t, dir)
	s := baseSpec()
	s.StoreDir = filepath.Join(dir, "nats")
	s.HubURL = "tls://hub.example:7442"
	s.HubCAFile = ca
	conf, err := RenderConf(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(conf, "ca_file") {
		t.Error("ca_file rendered with no leaf seed; the leaf block must not render at all")
	}
	s.LeafSeed = true
	conf, err = RenderConf(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{ urls: ["tls://hub.example:7442"], nkey: $DIRECTOR_LEAF_NKEY, tls { ca_file: "` + ca + `" } }`
	if !strings.Contains(conf, want) {
		t.Fatalf("leaf remote lacks the tls block:\n%s", conf)
	}
	if strings.Count(conf, "ca_file") != 1 {
		t.Errorf("ca_file appears outside the leaf remote:\n%s", conf)
	}

	ns, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	auth, err := RenderAuth(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFiles(s.StoreDir, conf, auth); err != nil {
		t.Fatal(err)
	}
	kp, _ := nkeys.CreateUser()
	seed, _ := kp.Seed()
	cmd := exec.Command(ns, "-t", "-c", filepath.Join(s.StoreDir, ConfName))
	cmd.Env = append(os.Environ(), LeafSeedEnv+"="+string(seed))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nats-server -t rejected the conf with a tls block: %v\n%s\n%s", err, out, conf)
	}
}

// The seat password file follows the seat: written 0600 when a seat is
// declared, stable across regenerations, gone when the seat is removed.
// And RecoverPasswords reads every line the renderer wrote.
func TestManagerSeatPassFileAndRecovery(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	seat := &config.Seat{Workspace: "aae-orc", Team: "ops"}
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", StoreDir: dir, Seat: seat}
	live := &teams{{Name: "ops", Workspace: "aae-orc", Roles: []api.Role{{Name: "supervisor"}}}}
	m, err := NewManager(dir, "kinu", rb, live, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	user, pw, ok := m.SeatCredential()
	if !ok || user != SeatUserName || pw == "" {
		t.Fatalf("seat credential = %q %q %v", user, pw, ok)
	}
	body, err := os.ReadFile(m.SeatPassPath())
	if err != nil {
		t.Fatalf("seat pass file: %v", err)
	}
	if string(body) != pw+"\n" {
		t.Errorf("seat pass file holds %q, want the seat password", body)
	}
	if st, _ := os.Stat(m.SeatPassPath()); st.Mode().Perm() != 0o600 {
		t.Errorf("seat pass file mode %v, want 0600", st.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 {
		t.Errorf("state dir holds %d entries, want conf, auth, and the pass file (no temp left behind)", len(entries))
	}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if _, pw2, _ := m.SeatCredential(); pw2 != pw {
		t.Error("seat password changed across regenerations")
	}

	// A successor over the same directory recovers admin, seat, and team.
	got := RecoverPasswords(filepath.Join(dir, AuthName))
	opsPw, _ := m.TeamPassword("ops")
	if got[AdminUser] != m.Admin().Password || got[SeatUserName] != pw || got["ops"] != opsPw || len(got) != 3 {
		t.Errorf("recovered %d passwords = %v", len(got), got)
	}
	m2, err := NewManager(dir, "kinu", rb, live, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, pw2, _ := m2.SeatCredential(); pw2 != pw {
		t.Error("successor minted a fresh seat password")
	}
	if changed, _ := m2.Regenerate(); changed {
		t.Error("successor rewrote files that had not changed")
	}

	// No seat: the user line and the file go.
	rb.Seat = nil
	m3, err := NewManager(dir, "kinu", rb, live, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := m3.Regenerate(); err != nil || !changed {
		t.Fatalf("removing the seat: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(m3.SeatPassPath()); !os.IsNotExist(err) {
		t.Errorf("seat pass file survives the seat's removal: %v", err)
	}
	auth, _ := os.ReadFile(filepath.Join(dir, AuthName))
	if strings.Contains(string(auth), "user: director") {
		t.Error("director user survives the seat's removal")
	}
	if _, _, ok := m3.SeatCredential(); ok {
		t.Error("manager without a seat reports a seat credential")
	}
	// Nothing to recover from an absent file.
	if n := len(RecoverPasswords(filepath.Join(dir, "missing"))); n != 0 {
		t.Errorf("recovered %d from a missing file", n)
	}
}
