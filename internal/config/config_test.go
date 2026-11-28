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
	hub := &Hub{URL: "nats-leaf://192.0.2.110:7442"}
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
	body := "clusters:\n  - name: kinu\n    bus:\n      managed: true\n      listen: 127.0.0.1:4222\n      hub:\n        url: nats-leaf://192.0.2.110:7442\n  - name: broken\n    bus:\n      managed: true\ncurrent_cluster: kinu\n"
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
	if b == nil || !b.Managed || b.Listen != "127.0.0.1:4222" || b.Hub == nil || b.Hub.URL != "nats-leaf://192.0.2.110:7442" {
		t.Errorf("bus section not parsed intact: %+v", b)
	}
}

// The account name map holds display text only; a value shaped like a
// credential is refused at load, and the error never echoes it.
func TestAccountNamesRefuseATokenShapedValue(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"sk-ant-api03-AbCdEf0123456789xyzAbCdEf",
		"ghp_1234567890abcdefghijklmnopqrstuvwxyz",
		"eyJhbGciOiJIUzI1NiJ9abcdef0123456789",
		"AKIAABCDEFGHIJKLMNOP1234",
	} {
		cfg := &Config{Display: Display{AccountNames: map[string]string{"ab12cd": bad}}}
		err := cfg.Display.CheckAccountNames()
		if err == nil {
			t.Errorf("a token-shaped value %q passed Validate", bad[:8])
			continue
		}
		if strings.Contains(err.Error(), bad) {
			t.Errorf("the error echoes the value: %v", err)
		}
	}
}

func TestAccountNamesAcceptPlainDisplayText(t *testing.T) {
	t.Parallel()
	cfg := &Config{Display: Display{AccountNames: map[string]string{
		"ab12cd": "work", "k2/ab12cd": "alt ultra", "0f9e8d": "Ultra-2",
	}}}
	if err := cfg.Display.CheckAccountNames(); err != nil {
		t.Errorf("plain display names were refused: %v", err)
	}
}

// The shape that is refused is a credential's: a known prefix, or one unbroken run
// of 32 or more token characters with both a letter and a digit. A long plain
// word with no digit is display text, and a short mixed name is too.
func TestAccountNamesTokenShapeIsDecidedOnPurpose(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value  string
		refuse bool
	}{
		{"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8", true},
		{"internationalization-department-name", false},
		{"ultra-2", false},
		{"work account 2", false},
		{"a1b2c3d4e5f6a7b8 c9d0e1f2a3b4c5d6e7f8", false},
		// Director's ruling (option 2): a run counts only when it holds no - or _.
		{"platform-team-production-account-02", false},
		{"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a1b2", true},
	}
	for _, c := range cases {
		cfg := &Config{Display: Display{AccountNames: map[string]string{"ab12cd": c.value}}}
		if err := cfg.Display.CheckAccountNames(); (err != nil) != c.refuse {
			t.Errorf("%q: Validate error = %v, want refused %v", c.value, err, c.refuse)
		}
	}
}

// A name map key is an account id, optionally cluster-qualified; anything else
// is refused so a typo cannot silently match nothing.
func TestAccountNamesRefuseAMalformedKey(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"", "ab12c", "ab12cdz", "k1/", "/ab12cd", "k1/k2/ab12cd", "AB12CD"} {
		cfg := &Config{Display: Display{AccountNames: map[string]string{key: "work"}}}
		if err := cfg.Display.CheckAccountNames(); err == nil {
			t.Errorf("the key %q passed Validate", key)
		}
	}
	for _, key := range []string{"ab12cd", "k1/ab12cd", "my-cluster.2/0f9e8d"} {
		cfg := &Config{Display: Display{AccountNames: map[string]string{key: "work"}}}
		if err := cfg.Display.CheckAccountNames(); err != nil {
			t.Errorf("the key %q was refused: %v", key, err)
		}
	}
}

// The error for a refused name never carries the value, for each credential
// shape the map refuses.
func TestAccountNameErrorsNeverEchoTheValue(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"sk-ant-api03-AbCdEf0123456789xyzAbCdEf",
		"ghp_1234567890abcdefghijklmnopqrstuvwxyz",
		"eyJhbGciOiJIUzI1NiJ9abcdef0123456789",
		"AKIAABCDEFGHIJKLMNOP1234",
		"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8",
	} {
		cfg := &Config{Display: Display{AccountNames: map[string]string{"ab12cd": bad}}}
		err := cfg.Display.CheckAccountNames()
		if err == nil {
			t.Errorf("%q passed", bad[:8])
			continue
		}
		if strings.Contains(err.Error(), bad) || strings.Contains(err.Error(), bad[:12]) {
			t.Errorf("the error echoes the value: %v", err)
		}
	}
}

// A value is trimmed text with no control character; an empty value is refused.
// A name equal to another account's key is allowed: it is display text.
func TestAccountNameValuesAreCleanText(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", " work", "work ", "wo\nrk", "wo\x1b[31mrk"} {
		cfg := &Config{Display: Display{AccountNames: map[string]string{"ab12cd": bad}}}
		if cfg.Display.CheckAccountNames() == nil {
			t.Errorf("the value %q passed Validate", bad)
		}
	}
	cfg := &Config{Display: Display{AccountNames: map[string]string{"ab12cd": "0f9e8d", "0f9e8d": "other"}}}
	if err := cfg.Display.CheckAccountNames(); err != nil {
		t.Errorf("a name equal to another account's key was refused: %v", err)
	}
}

// The refusals carry the sentinel, so a caller that can do without a display
// map matches it with errors.Is.
func TestAccountNameRefusalsAreTheSentinel(t *testing.T) {
	t.Parallel()
	for name, names := range map[string]map[string]string{
		"malformed key":    {"nope": "work"},
		"padded value":     {"ab12cd": " work"},
		"credential shape": {"ab12cd": "ghp_1234567890abcdefghijklmnopqrstuvwxyz"},
	} {
		cfg := &Config{Display: Display{AccountNames: names}}
		if err := cfg.Display.CheckAccountNames(); !errors.Is(err, ErrInvalidAccountNames) {
			t.Errorf("%s: CheckAccountNames error %v does not wrap ErrInvalidAccountNames", name, err)
		}
	}
}

// A refused name map is a display mistake, not a load failure: Load returns no
// error for it, so no caller that refuses on any error can turn it into an
// unreadable config. The refusal rides on the config, and the file's own entry is
// kept so a command that loads and saves the config does not erase it.
func TestLoadKeepsADisplayRefusalOffTheError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{
		Clusters: []Cluster{{Name: "a", Socket: "/scratch/a.sock"}},
		Display:  Display{AccountNames: map[string]string{"ab12cd": " padded"}},
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned %v for a refused display name, want no error", err)
	}
	if !errors.Is(got.AccountNamesRefused, ErrInvalidAccountNames) {
		t.Errorf("AccountNamesRefused = %v, want it to wrap ErrInvalidAccountNames", got.AccountNamesRefused)
	}
	if err := Save(got); err != nil {
		t.Fatal(err)
	}
	again, _ := Load()
	if again == nil || again.Display.AccountNames["ab12cd"] != " padded" {
		t.Errorf("a load and save dropped the operator's entry: %+v", again)
	}
}

// A good map loads with no refusal, and a bad cluster name still comes back as
// its own error, with no account-name sentinel in it.
func TestLoadKeepsTheClusterErrorAndOnlyThatError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{
		Clusters: []Cluster{{Name: "bad name", Socket: "/scratch/a.sock"}},
		Display:  Display{AccountNames: map[string]string{"ab12cd": " padded"}},
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if !errors.Is(err, ErrInvalidClusterName) || errors.Is(err, ErrInvalidAccountNames) {
		t.Errorf("Load error = %v, want the cluster-name error and not the account-name sentinel", err)
	}
}

// A good map, or none, leaves the refusal unset.
func TestLoadLeavesTheRefusalUnsetForAGoodMap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{
		Clusters: []Cluster{{Name: "a", Socket: "/scratch/a.sock"}},
		Display:  Display{AccountNames: map[string]string{"ab12cd": "work"}},
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || got.AccountNamesRefused != nil {
		t.Errorf("Load = %v, refusal %v; want no error and no refusal", err, got.AccountNamesRefused)
	}
}

// allow_remote_attach is the daemon-side switch for a terminal on the mrvl://
// listener. It reads like allow_credential_reveal: a cluster entry key,
// false when absent.
func TestClusterAllowRemoteAttachParses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".marvel"), 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "clusters:\n  - name: on\n    socket: /tmp/m-on.sock\n    allow_remote_attach: true\n  - name: off\n    socket: /tmp/m-off.sock\n"
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := map[string]bool{}
	for _, c := range cfg.Clusters {
		got[c.Name] = c.AllowRemoteAttach
	}
	if !got["on"] || got["off"] {
		t.Errorf("allow_remote_attach by cluster = %v, want on true and off false", got)
	}
}
