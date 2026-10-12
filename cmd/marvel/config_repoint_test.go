package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/knownhosts"
	"github.com/arcavenae/marvel/internal/paths"
)

const (
	repointOld = "mrvl://op@10.0.0.5:6785"
	repointNew = "mrvl://op@10.0.0.77:6785"
)

func repointKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// repointEnv is a scratch home with cluster "prod" at repointOld, whose host
// key is recorded, and an unrelated cluster "other". The probe is replaced by
// a map from "host:port" to the key that address presents.
type repointEnv struct {
	home      string
	recorded  ssh.PublicKey
	presented map[string]ssh.PublicKey
	probed    []string
	probeErr  error
}

func newRepointEnv(t *testing.T) *repointEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_SOCKET", "")
	e := &repointEnv{home: home, recorded: repointKey(t), presented: map[string]ssh.PublicKey{}}
	if err := config.Save(&config.Config{
		Clusters: []config.Cluster{
			{Name: "prod", Server: repointOld, Identity: "/keys/prod"},
			{Name: "other", Server: "mrvl://op@10.0.0.9:6785", Identity: "/keys/other"},
			{Name: "sock", Socket: "/run/marvel.sock"},
		},
		CurrentCluster: "prod",
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	if err := knownhosts.Trust(layout, "10.0.0.5:6785", e.recorded); err != nil {
		t.Fatal(err)
	}
	old := repointProbe
	repointProbe = func(addr string) (ssh.PublicKey, error) {
		e.probed = append(e.probed, addr)
		if e.probeErr != nil {
			return nil, e.probeErr
		}
		hp := strings.TrimPrefix(addr, "mrvl://")
		if i := strings.Index(hp, "@"); i >= 0 {
			hp = hp[i+1:]
		}
		k, ok := e.presented[hp]
		if !ok {
			return nil, errors.New("nothing presented at " + hp)
		}
		return k, nil
	}
	t.Cleanup(func() { repointProbe = old })
	return e
}

func (e *repointEnv) file(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.home, ".marvel", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func (e *repointEnv) cluster(t *testing.T, name string) config.Cluster {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cl, err := cfg.GetCluster(name)
	if err != nil || cl == nil {
		t.Fatalf("cluster %q: %v", name, err)
	}
	return *cl
}

func (e *repointEnv) recordedFor(t *testing.T, hostport string) []ssh.PublicKey {
	t.Helper()
	layout, _ := paths.Default()
	keys, err := knownhosts.Recorded(layout, hostport)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// state captures every file a repoint may write, so a refusal can be shown to
// have written nothing.
func (e *repointEnv) state(t *testing.T) string {
	t.Helper()
	return e.file(t, "config.yaml") + "\x00" + e.file(t, "known_hosts")
}

func TestRepointMovesTheClusterWhenTheKeyMatchesAndTheOperatorConfirms(t *testing.T) {
	e := newRepointEnv(t)
	e.presented["10.0.0.77:6785"] = e.recorded
	var out bytes.Buffer

	if err := runRepoint(&out, strings.NewReader("y\n"), true, "prod", repointNew); err != nil {
		t.Fatalf("runRepoint: %v\n%s", err, out.String())
	}

	cl := e.cluster(t, "prod")
	if cl.Server != repointNew {
		t.Errorf("prod server = %q, want %q", cl.Server, repointNew)
	}
	if cl.Identity != "/keys/prod" {
		t.Errorf("prod identity = %q, want it kept", cl.Identity)
	}
	if got := e.recordedFor(t, "10.0.0.77:6785"); len(got) != 1 || !bytes.Equal(got[0].Marshal(), e.recorded.Marshal()) {
		t.Errorf("the new address holds %d keys, want the carried one", len(got))
	}
	if got := e.recordedFor(t, "10.0.0.5:6785"); len(got) != 1 {
		t.Errorf("the old address entry was changed: %d keys", len(got))
	}
	if other := e.cluster(t, "other"); other.Server != "mrvl://op@10.0.0.9:6785" || other.Identity != "/keys/other" {
		t.Errorf("an unrelated cluster changed: %+v", other)
	}
}

func TestRepointPromptNamesBothAddressesAndTheFingerprint(t *testing.T) {
	e := newRepointEnv(t)
	e.presented["10.0.0.77:6785"] = e.recorded
	var out bytes.Buffer

	_ = runRepoint(&out, strings.NewReader("y\n"), true, "prod", repointNew)

	text := out.String()
	for _, want := range []string{"10.0.0.5:6785", "10.0.0.77:6785", ssh.FingerprintSHA256(e.recorded), "[y/N]"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt output lacks %q:\n%s", want, text)
		}
	}
}

func TestRepointAsksBeforeWritingAndDeclinesOnAnythingButYes(t *testing.T) {
	for _, tc := range []struct {
		answer string
		moves  bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"n\n", false},
		{"\n", false},
		{"", false},
		{"maybe\n", false},
		{"yy\n", false},
	} {
		e := newRepointEnv(t)
		e.presented["10.0.0.77:6785"] = e.recorded
		before := e.state(t)

		err := runRepoint(&bytes.Buffer{}, strings.NewReader(tc.answer), true, "prod", repointNew)

		if tc.moves {
			if err != nil || e.cluster(t, "prod").Server != repointNew {
				t.Errorf("answer %q: err=%v server=%q, want a move", tc.answer, err, e.cluster(t, "prod").Server)
			}
			continue
		}
		if err == nil {
			t.Errorf("answer %q: no error, want a refusal", tc.answer)
		}
		if after := e.state(t); after != before {
			t.Errorf("answer %q: a declined repoint wrote files", tc.answer)
		}
	}
}

func TestRepointRefusesAKeyThatDiffersFromTheRecordedOneAndWritesNothing(t *testing.T) {
	e := newRepointEnv(t)
	stranger := repointKey(t)
	e.presented["10.0.0.77:6785"] = stranger
	before := e.state(t)
	var out bytes.Buffer
	in := strings.NewReader("y\n")

	err := runRepoint(&out, in, true, "prod", repointNew)

	if err == nil {
		t.Fatal("a different key was accepted")
	}
	for _, want := range []string{ssh.FingerprintSHA256(e.recorded), ssh.FingerprintSHA256(stranger)} {
		if !strings.Contains(out.String()+err.Error(), want) {
			t.Errorf("the refusal does not print %s:\n%s\n%v", want, out.String(), err)
		}
	}
	if in.Len() != len("y\n") {
		t.Errorf("the command read the answer on a mismatch (%d bytes left); it must not ask", in.Len())
	}
	if after := e.state(t); after != before {
		t.Error("a mismatch changed config or known_hosts")
	}
}

func TestRepointRefusesWhenNoKeyIsRecordedForTheOldAddress(t *testing.T) {
	e := newRepointEnv(t)
	e.presented["10.0.0.77:6785"] = e.recorded
	cfg, _ := config.Load()
	if err := cfg.AddCluster("fresh", "mrvl://op@10.0.0.200:6785", ""); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	before := e.state(t)

	err := runRepoint(&bytes.Buffer{}, strings.NewReader("y\n"), true, "fresh", repointNew)

	if err == nil {
		t.Fatal("repoint carried trust that was never recorded")
	}
	if !strings.Contains(err.Error(), "keys trust") {
		t.Errorf("error %q should point at the keys trust path", err)
	}
	if len(e.probed) != 0 {
		t.Errorf("probed %v with nothing to compare against", e.probed)
	}
	if after := e.state(t); after != before {
		t.Error("a refused repoint wrote files")
	}
}

func TestRepointNeedsATerminalAndWritesNothingWithout(t *testing.T) {
	e := newRepointEnv(t)
	e.presented["10.0.0.77:6785"] = e.recorded
	before := e.state(t)
	var out bytes.Buffer

	err := runRepoint(&out, strings.NewReader("y\n"), false, "prod", repointNew)

	if err == nil {
		t.Fatal("repoint ran without a terminal")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("error %q should say a terminal is needed", err)
	}
	if after := e.state(t); after != before {
		t.Error("a non-interactive repoint wrote files")
	}
}

func TestRepointHasNoYesFlag(t *testing.T) {
	if f := repointCmd().Flags().Lookup("yes"); f != nil {
		t.Fatal("config repoint has a --yes flag: each new address is confirmed by a person")
	}
}

func TestRepointDoesNotDuplicateAKeyAlreadyRecordedForTheNewAddress(t *testing.T) {
	e := newRepointEnv(t)
	e.presented["10.0.0.77:6785"] = e.recorded
	layout, _ := paths.Default()
	if err := knownhosts.Trust(layout, "10.0.0.77:6785", e.recorded); err != nil {
		t.Fatal(err)
	}

	if err := runRepoint(&bytes.Buffer{}, strings.NewReader("y\n"), true, "prod", repointNew); err != nil {
		t.Fatal(err)
	}

	if got := e.recordedFor(t, "10.0.0.77:6785"); len(got) != 1 {
		t.Errorf("new address holds %d copies of the key, want 1", len(got))
	}
	if e.cluster(t, "prod").Server != repointNew {
		t.Error("the cluster was not moved")
	}
}

func TestRepointRefusesAClusterThatIsNotAnMrvlAddress(t *testing.T) {
	e := newRepointEnv(t)
	before := e.state(t)

	err := runRepoint(&bytes.Buffer{}, strings.NewReader("y\n"), true, "sock", repointNew)

	if err == nil {
		t.Fatal("a socket cluster was repointed")
	}
	if after := e.state(t); after != before {
		t.Error("a refused repoint wrote files")
	}
}

func TestRepointRefusesAnUnknownCluster(t *testing.T) {
	e := newRepointEnv(t)
	before := e.state(t)
	if err := runRepoint(&bytes.Buffer{}, strings.NewReader("y\n"), true, "nope", repointNew); err == nil {
		t.Fatal("an unknown cluster was repointed")
	}
	if after := e.state(t); after != before {
		t.Error("a refused repoint wrote files")
	}
}

func TestRepointRefusesANewAddressThatCannotBeDialedDirectly(t *testing.T) {
	e := newRepointEnv(t)
	before := e.state(t)
	for _, bad := range []string{"/run/marvel.sock", "tcp://10.0.0.77:6785", "ssh://op@10.0.0.77/run/marvel.sock"} {
		if err := runRepoint(&bytes.Buffer{}, strings.NewReader("y\n"), true, "prod", bad); err == nil {
			t.Errorf("repoint to %q was accepted", bad)
		}
	}
	if after := e.state(t); after != before {
		t.Error("a refused repoint wrote files")
	}
}

func TestRepointRefusesTheAddressTheClusterAlreadyHas(t *testing.T) {
	e := newRepointEnv(t)
	before := e.state(t)
	err := runRepoint(&bytes.Buffer{}, strings.NewReader("y\n"), true, "prod", repointOld)
	if err == nil {
		t.Fatal("repoint to the same address succeeded")
	}
	if after := e.state(t); after != before {
		t.Error("a no-op repoint wrote files")
	}
}

func TestRepointReportsAProbeFailureAndWritesNothing(t *testing.T) {
	e := newRepointEnv(t)
	e.probeErr = errors.New("dial tcp: connection refused")
	before := e.state(t)

	err := runRepoint(&bytes.Buffer{}, strings.NewReader("y\n"), true, "prod", repointNew)

	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want the probe's cause", err)
	}
	if after := e.state(t); after != before {
		t.Error("a failed probe wrote files")
	}
}

func TestRepointIsListedUnderConfig(t *testing.T) {
	cfg := configCmd()
	found, _, err := cfg.Find([]string{"repoint"})
	if err != nil || found == nil || found.Name() != "repoint" {
		t.Fatalf("config has no repoint subcommand: %v", err)
	}
}
