package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/paths"
)

func TestParseScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    Scope
		wantErr bool
	}{
		{"", ScopeAdmin, false},
		{"admin", ScopeAdmin, false},
		{"credential-push", ScopeCredentialPush, false},
		{"Admin", "", true},
		{"push", "", true},
		{"root", "", true},
	}
	for _, tt := range tests {
		got, err := ParseScope(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseScope(%q): want error, got %q", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseScope(%q): unexpected error %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseScope(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestScopeFromOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		options []string
		want    Scope
		wantErr bool
	}{
		{"none is admin", nil, ScopeAdmin, false},
		{"unrelated option is admin", []string{`environment="X=1"`}, ScopeAdmin, false},
		{"explicit admin", []string{`marvel-scope="admin"`}, ScopeAdmin, false},
		{"credential-push", []string{`marvel-scope="credential-push"`}, ScopeCredentialPush, false},
		{"credential-push beside another", []string{`marvel-scope="credential-push"`, `environment="X=1"`}, ScopeCredentialPush, false},
		{"malformed fails closed", []string{`marvel-scope="root"`}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := scopeFromOptions(tt.options)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMethodAllowedForScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		scope  Scope
		method string
		want   bool
	}{
		{ScopeAdmin, "apply", true},
		{ScopeAdmin, "delete", true},
		{ScopeAdmin, "inject", true},
		{ScopeAdmin, "credential.put", true},
		{ScopeCredentialPush, "credential.put", true},
		{ScopeCredentialPush, "credential.list", true},
		{ScopeCredentialPush, "credential.delete", true},
		{ScopeCredentialPush, "credential.get", false}, // reveal is never a pushing key's reach
		{ScopeCredentialPush, "apply", false},
		{ScopeCredentialPush, "inject", false},
		{ScopeCredentialPush, "logs", false},
		{Scope(""), "credential.put", false}, // unknown scope permits nothing
		{Scope("root"), "logs", false},
	}
	for _, tt := range tests {
		if got := methodAllowedForScope(tt.method, tt.scope); got != tt.want {
			t.Errorf("methodAllowedForScope(%q, %q) = %v, want %v", tt.method, tt.scope, got, tt.want)
		}
	}
}

// testPubKeyLine returns a fresh ed25519 public key in authorized_keys form.
func testPubKeyLine(t *testing.T) []byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}
	return ssh.MarshalAuthorizedKey(sshPub)
}

func TestLoadAuthorizedKeysScope(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	k1 := strings.TrimSpace(string(testPubKeyLine(t))) // unscoped: admin
	k2 := strings.TrimSpace(string(testPubKeyLine(t))) // credential-push
	k3 := strings.TrimSpace(string(testPubKeyLine(t))) // malformed scope: skipped
	content := k1 + "\n" +
		`marvel-scope="credential-push" ` + k2 + "\n" +
		`marvel-scope="root" ` + k3 + "\n"
	if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := loadAuthorizedKeys(paths.WithHome(dir))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (the malformed-scope key must be skipped)", len(entries))
	}
	if entries[0].scope != ScopeAdmin {
		t.Errorf("first key scope = %q, want admin (no option)", entries[0].scope)
	}
	if entries[1].scope != ScopeCredentialPush {
		t.Errorf("second key scope = %q, want credential-push", entries[1].scope)
	}
}

func TestAddAndListAuthorizedKeyScope(t *testing.T) {
	// Not parallel: overrides HOME so paths.Default resolves into a temp dir.
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if err := AddAuthorizedKey(testPubKeyLine(t), "operator", ScopeCredentialPush); err != nil {
		t.Fatalf("add: %v", err)
	}

	authed, err := ListAuthorizedKeys()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(authed) != 1 {
		t.Fatalf("got %d authorized keys, want 1", len(authed))
	}
	if authed[0].Scope != string(ScopeCredentialPush) {
		t.Errorf("listed scope = %q, want credential-push", authed[0].Scope)
	}

	// The on-disk line must carry the scope back through the auth loader too.
	entries, err := loadAuthorizedKeys(paths.WithHome(filepath.Join(dir, ".marvel")))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(entries) != 1 || entries[0].scope != ScopeCredentialPush {
		t.Fatalf("reloaded entries = %+v, want one credential-push key", entries)
	}
}

func TestDispatchAsScopeEnforcement(t *testing.T) {
	d := newHandlerDaemon(t)

	const refusal = "is not permitted for a"

	// A credential-push key cannot reach an admin method.
	resp := d.dispatchAs(Request{Method: "logs"}, caller{scope: ScopeCredentialPush, fingerprint: "SHA256:test"})
	if !strings.Contains(resp.Error, refusal) {
		t.Errorf("credential-push on logs: error = %q, want a scope refusal", resp.Error)
	}

	// An admin key (and the local socket) reaches it.
	resp = d.dispatchAs(Request{Method: "logs"}, localCaller())
	if strings.Contains(resp.Error, refusal) {
		t.Errorf("admin on logs: unexpected scope refusal %q", resp.Error)
	}

	// A credential-push method passes the scope gate today; it fails only as an
	// unknown method until the credential.* handlers land (aae-orc-gdum6), never
	// as a scope refusal.
	resp = d.dispatchAs(Request{Method: "credential.put"}, caller{scope: ScopeCredentialPush, fingerprint: "SHA256:test"})
	if strings.Contains(resp.Error, refusal) {
		t.Errorf("credential-push on credential.put: unexpected scope refusal %q", resp.Error)
	}
	if !strings.Contains(resp.Error, "unknown method") {
		t.Errorf("credential.put before its handler exists: error = %q, want unknown method", resp.Error)
	}

	// An empty or unrecognized scope permits nothing.
	resp = d.dispatchAs(Request{Method: "logs"}, caller{scope: Scope(""), fingerprint: "SHA256:test"})
	if !strings.Contains(resp.Error, refusal) {
		t.Errorf("empty scope on logs: error = %q, want a scope refusal", resp.Error)
	}
}
