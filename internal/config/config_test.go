package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDaemonHomeWarning(t *testing.T) {
	t.Parallel()

	const (
		mine   = "/Users/op/.marvel"
		theirs = "/Users/other/.marvel"
		sock   = "/Users/op/.marvel/run/marvel.sock"
	)

	tests := []struct {
		name       string
		addr       string
		daemonHome string
		clientHome string
		wantWarn   bool
	}{
		{
			name:       "mismatch on a local socket warns",
			addr:       sock,
			daemonHome: theirs,
			clientHome: mine,
			wantWarn:   true,
		},
		{
			name:       "matching homes stay quiet",
			addr:       sock,
			daemonHome: mine,
			clientHome: mine,
		},
		{
			name:       "remote mrvl cluster is expected to differ",
			addr:       "mrvl://other-host",
			daemonHome: theirs,
			clientHome: mine,
		},
		{
			name:       "remote ssh cluster is expected to differ",
			addr:       "ssh://op@other-host/home/op/.marvel/run/marvel.sock",
			daemonHome: theirs,
			clientHome: mine,
		},
		{
			name:       "explicit tcp is expected to differ",
			addr:       "tcp://127.0.0.1:9090",
			daemonHome: theirs,
			clientHome: mine,
		},
		{
			name:       "bare host:port is expected to differ",
			addr:       "127.0.0.1:9090",
			daemonHome: theirs,
			clientHome: mine,
		},
		{
			name:       "daemon predating the field says nothing",
			addr:       sock,
			daemonHome: "",
			clientHome: mine,
		},
		{
			name:       "no client home leaves nothing to compare",
			addr:       sock,
			daemonHome: theirs,
			clientHome: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DaemonHomeWarning(tt.addr, tt.daemonHome, tt.clientHome)
			if tt.wantWarn && got == "" {
				t.Fatalf("expected a warning for addr %q, daemon %q, client %q",
					tt.addr, tt.daemonHome, tt.clientHome)
			}
			if !tt.wantWarn && got != "" {
				t.Fatalf("expected no warning, got %q", got)
			}
		})
	}
}

// The warning has to be actionable on its own: an operator reading one
// line of stderr needs both homes and the way out.
func TestDaemonHomeWarningNamesBothHomesAndTheOverride(t *testing.T) {
	t.Parallel()

	const (
		mine   = "/Users/op/.marvel"
		theirs = "/Users/other/.marvel"
		sock   = "/Users/op/.marvel/run/marvel.sock"
	)

	w := DaemonHomeWarning(sock, theirs, mine)
	if w == "" {
		t.Fatal("expected a warning")
	}
	for _, want := range []string{theirs, mine, sock, "--socket", SocketEnv} {
		if !strings.Contains(w, want) {
			t.Errorf("warning does not mention %q: %s", want, w)
		}
	}
}

func TestIsLocalSocket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr string
		want bool
	}{
		{"/Users/op/.marvel/run/marvel.sock", true},
		{"/tmp/marvel.sock", true},
		{"mrvl://host", false},
		{"mrvl://op@host:6785", false},
		{"ssh://op@host/run/marvel.sock", false},
		{"tcp://host:9090", false},
		{"host:9090", false},
		{":9090", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()
			if got := isLocalSocket(tt.addr); got != tt.want {
				t.Errorf("isLocalSocket(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestValidateClusterName(t *testing.T) {
	t.Parallel()
	accepted := []string{"local", "kinu-1", "ops_2", "A-Z09_", "-", "_"}
	for _, name := range accepted {
		if err := ValidateClusterName(name); err != nil {
			t.Errorf("ValidateClusterName(%q) = %v, want accepted", name, err)
		}
	}
	rejected := map[string]struct{ name, byte_ string }{
		"dot separator":  {"my.cluster", "."},
		"space":          {"my cluster", " "},
		"star":           {"ops*", "*"},
		"trailing gt":    {"ops>", ">"},
		"tab":            {"op\ts", "\t"},
		"unicode letter": {"plannér", "é"},
		"slash":          {"a/b", "/"},
	}
	for label, tc := range rejected {
		err := ValidateClusterName(tc.name)
		if err == nil {
			t.Errorf("%s: ValidateClusterName(%q) = nil, want rejection", label, tc.name)
			continue
		}
		if !errors.Is(err, ErrInvalidClusterName) {
			t.Errorf("%s: error %v is not ErrInvalidClusterName", label, err)
		}
		if !strings.Contains(err.Error(), strconv.Quote(tc.byte_)) {
			t.Errorf("%s: error %q does not name the offending byte %q", label, err, tc.byte_)
		}
	}
	if err := ValidateClusterName(""); !errors.Is(err, ErrInvalidClusterName) {
		t.Errorf("empty name: got %v, want ErrInvalidClusterName", err)
	}
}

func TestAddClusterRefusesBadNameWithoutWriting(t *testing.T) {
	t.Parallel()
	cfg := defaultConfig()
	before := len(cfg.Clusters)
	err := cfg.AddCluster("my.cluster", "mrvl://u@h", "")
	if !errors.Is(err, ErrInvalidClusterName) {
		t.Fatalf("AddCluster(my.cluster) = %v, want ErrInvalidClusterName", err)
	}
	if len(cfg.Clusters) != before {
		t.Errorf("a refused name was still added: %+v", cfg.Clusters)
	}
	if err := cfg.AddCluster("remote-1", "mrvl://u@h", ""); err != nil {
		t.Fatalf("AddCluster(remote-1) = %v, want nil", err)
	}
	if len(cfg.Clusters) != before+1 {
		t.Errorf("valid name not added: %+v", cfg.Clusters)
	}
}

func TestLoadReportsEveryBadNameAndStillReturnsConfig(t *testing.T) {
	// Not parallel: overrides HOME so configPath resolves into a temp dir.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".marvel"), 0o700); err != nil {
		t.Fatal(err)
	}
	yamlBody := "clusters:\n  - name: good\n  - name: bad.one\n    server: mrvl://u@h\n  - name: \"bad two\"\ncurrent_cluster: good\n"
	if err := os.WriteFile(filepath.Join(dir, ".marvel", "config.yaml"), []byte(yamlBody), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if !errors.Is(err, ErrInvalidClusterName) {
		t.Fatalf("Load() error = %v, want ErrInvalidClusterName", err)
	}
	if cfg == nil {
		t.Fatal("Load() returned a nil config beside the validation error; callers that proceed per cluster need it")
	}
	// Every offender is named in one report, and the byte for each.
	for _, want := range []string{`"bad.one"`, `"."`, `"bad two"`, `" "`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load() error does not mention %s:\n%v", want, err)
		}
	}
	// Nothing was rewritten and the unaffected cluster is intact.
	if len(cfg.Clusters) != 3 || cfg.Clusters[1].Name != "bad.one" {
		t.Errorf("Load() rewrote or dropped clusters: %+v", cfg.Clusters)
	}
	if addr, err := cfg.ResolveCluster("good"); err != nil || addr == "" {
		t.Errorf("unaffected cluster does not resolve: addr=%q err=%v", addr, err)
	}
}

func TestValidateBus(t *testing.T) {
	t.Parallel()
	hub := &Hub{URL: "nats-leaf://192.168.100.110:7442"}
	cases := []struct {
		name string
		bus  *Bus
		ok   bool
		want string // substring the error must carry when !ok
	}{
		{"nil section", nil, true, ""},
		{"managed explicit", &Bus{Managed: true, Listen: "127.0.0.1:4222"}, true, ""},
		{"managed with hub", &Bus{Managed: true, Listen: "127.0.0.1:4222", Hub: hub}, true, ""},
		{"adopted by url", &Bus{URL: "nats://127.0.0.1:4222"}, true, ""},
		{"managed needs listen", &Bus{Managed: true}, false, "explicit listen"},
		{"listen not host:port", &Bus{Managed: true, Listen: "4222"}, false, "not host:port"},
		{"listen no host", &Bus{Managed: true, Listen: ":4222"}, false, "explicit host"},
		{"listen bad port", &Bus{Managed: true, Listen: "127.0.0.1:99999"}, false, "bad port"},
		{"adopted needs url", &Bus{}, false, "needs url"},
		{"url wrong scheme", &Bus{URL: "http://127.0.0.1:4222"}, false, "scheme"},
		{"hub wrong scheme", &Bus{Managed: true, Listen: "127.0.0.1:4222", Hub: &Hub{URL: "http://h:1"}}, false, "hub.url"},
		{"hub no host", &Bus{Managed: true, Listen: "127.0.0.1:4222", Hub: &Hub{URL: "nats-leaf://"}}, false, "no host"},
	}
	for _, tc := range cases {
		err := ValidateBus("kinu", tc.bus)
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("%s: want error, got nil", tc.name)
				continue
			}
			if !errors.Is(err, ErrInvalidBus) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: error %v does not carry ErrInvalidBus and %q", tc.name, err, tc.want)
			}
		}
	}
}

func TestBusResolveDefaultsWithoutRewriting(t *testing.T) {
	t.Parallel()
	b := &Bus{Managed: true, Listen: "127.0.0.1:4222", Hub: &Hub{URL: "nats-leaf://h:7442"}}
	r := b.Resolve("/state")
	if r.URL != "nats://127.0.0.1:4222" {
		t.Errorf("URL default = %q, want nats://<listen>", r.URL)
	}
	if r.StoreDir != filepath.Join("/state", "nats") {
		t.Errorf("StoreDir default = %q, want <state>/nats", r.StoreDir)
	}
	if r.HubURL != "nats-leaf://h:7442" || !r.Managed {
		t.Errorf("resolved = %+v, want hub and managed carried", r)
	}
	if b.URL != "" || b.StoreDir != "" {
		t.Errorf("Resolve wrote defaults back into the section: %+v", b)
	}
	explicit := &Bus{Managed: true, Listen: "0.0.0.0:4300", URL: "nats://kinu.local:4300", StoreDir: "/var/nats"}
	if r := explicit.Resolve("/state"); r.URL != "nats://kinu.local:4300" || r.StoreDir != "/var/nats" {
		t.Errorf("explicit values not preserved: %+v", r)
	}
}

func TestClusterForSocketMatchesOwnPathOnly(t *testing.T) {
	// Not parallel: ~ expansion and ResolveSocket read HOME.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv(SocketEnv, "")
	cfg := &Config{Clusters: []Cluster{
		{Name: "remote", Server: "mrvl://u@h", Bus: &Bus{URL: "nats://x:1"}},
		{Name: "twin", Socket: "~/.marvel/run/twin.sock", Bus: &Bus{Managed: true, Listen: "127.0.0.1:4223"}},
		{Name: "local", Bus: &Bus{Managed: true, Listen: "127.0.0.1:4222"}},
	}}
	if cl := cfg.ClusterForSocket(filepath.Join(dir, ".marvel", "run", "twin.sock")); cl == nil || cl.Name != "twin" {
		t.Errorf("twin socket resolved to %+v, want twin", cl)
	}
	if cl := cfg.ClusterForSocket(ResolveSocket()); cl == nil || cl.Name != "local" {
		t.Errorf("default socket resolved to %+v, want local (no socket, no server)", cl)
	}
	if cl := cfg.ClusterForSocket(filepath.Join(dir, "elsewhere.sock")); cl != nil {
		t.Errorf("unknown socket resolved to %+v, want nil", cl)
	}
}

func TestLoadCarriesBusSectionAndReportsBadBus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".marvel"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "clusters:\n  - name: kinu\n    bus:\n      managed: true\n      listen: 127.0.0.1:4222\n      hub:\n        url: nats-leaf://192.168.100.110:7442\n  - name: broken\n    bus:\n      managed: true\ncurrent_cluster: kinu\n"
	if err := os.WriteFile(filepath.Join(dir, ".marvel", "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if !errors.Is(err, ErrInvalidBus) {
		t.Fatalf("Load() error = %v, want ErrInvalidBus for the broken cluster", err)
	}
	if cfg == nil || len(cfg.Clusters) != 2 {
		t.Fatalf("Load() did not return the parsed config beside the error: %+v", cfg)
	}
	b := cfg.Clusters[0].Bus
	if b == nil || !b.Managed || b.Listen != "127.0.0.1:4222" || b.Hub == nil || b.Hub.URL != "nats-leaf://192.168.100.110:7442" {
		t.Errorf("bus section not parsed intact: %+v", b)
	}
}
