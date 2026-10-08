package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// SecretKey is one named pattern list, case-insensitive on the key name. The
// ruling (redaction-q1-env, option c) redacts only keys that look like secrets,
// so each pattern must redact and ordinary keys must stay visible.
func TestSecretKeyRedactsEachPattern(t *testing.T) {
	for _, key := range []string{
		"SERVICE_TOKEN", "service_token", "GitHubToken", "API_KEY", "api-key", "apiKey", "OPENAI_APIKEY",
		"DB_PASSWORD", "db_passwd", "SSH_PASSPHRASE", "CLIENT_SECRET", "AWS_SECRET_ACCESS_KEY",
		"AWS_ACCESS_KEY_ID", "accessKey", "PRIVATE_KEY", "privateKey", "KEY", "SIGNING_KEY", "ssh.key",
		"CREDENTIALS", "BEARER", "AUTH_HEADER", "COOKIE", "SESSION_COOKIE", "GH_PAT", "NPM_PAT",
	} {
		if !SecretKey(key) {
			t.Errorf("SecretKey(%q) = false, want true", key)
		}
	}
}

func TestSecretKeyLeavesOrdinaryKeysVisible(t *testing.T) {
	for _, key := range []string{
		"REGION", "AWS_REGION", "HOME", "PATH", "LOG_LEVEL", "TERM", "EDITOR", "MARVEL_ROLE",
		"KEYBOARD", "MONKEY", "KEYMAP", "TURKEY", "PATIENT", "PATH_EXTRA", "",
	} {
		if SecretKey(key) {
			t.Errorf("SecretKey(%q) = true, want false", key)
		}
	}
}

func envRuntime(env map[string]string) Runtime {
	return Runtime{Command: "sleep", Args: []string{"--api-key", "visible-arg"}, Prompt: "visible-prompt", Env: env}
}

// Env values of secret-looking keys print as (redacted); the key stays, and an
// ordinary key's value is visible. Args and Prompt are untouched (ruling
// redaction-q2-args, option a).
func TestRedactReplacesOnlySecretLookingEnvValues(t *testing.T) {
	in := envRuntime(map[string]string{"SERVICE_TOKEN": "canary-secret", "REGION": "us-east-1"})
	out := Redact(in)
	if out.Env["SERVICE_TOKEN"] != Redacted {
		t.Errorf("secret key value = %q, want %q", out.Env["SERVICE_TOKEN"], Redacted)
	}
	if out.Env["REGION"] != "us-east-1" {
		t.Errorf("ordinary key value = %q, want it visible", out.Env["REGION"])
	}
	if len(out.Args) != 2 || out.Args[1] != "visible-arg" || out.Prompt != "visible-prompt" {
		t.Errorf("Args or Prompt changed: %v %q", out.Args, out.Prompt)
	}
}

// Redact returns a copy: the stored record keeps the value (the durable record
// keeps what marvel needs; the wire gets a view).
func TestRedactNeverChangesItsInput(t *testing.T) {
	env := map[string]string{"SERVICE_TOKEN": "canary-secret"}
	in := envRuntime(env)
	_ = Redact(in)
	if env["SERVICE_TOKEN"] != "canary-secret" || in.Env["SERVICE_TOKEN"] != "canary-secret" {
		t.Fatal("Redact wrote through to its input")
	}
}

// Redact finds a Runtime at any depth, by record type: in a session, in a
// role inside a team, in a slice, a map, a pointer and an interface.
func TestRedactReachesEveryRuntimeAtAnyDepth(t *testing.T) {
	rt := envRuntime(map[string]string{"SERVICE_TOKEN": "canary-secret"})
	team := Team{Name: "t", Roles: []Role{{Name: "r", Runtime: rt}}}
	type wrapper struct {
		Sessions []Session
		ByName   map[string]Session
		Ptr      *Session
		Any      any
		Anon     map[string]any
	}
	sess := Session{Name: "s", Runtime: rt}
	w := wrapper{
		Sessions: []Session{sess},
		ByName:   map[string]Session{"k": sess},
		Ptr:      &sess,
		Any:      []Team{team},
		Anon:     map[string]any{"deep": []any{map[string]any{"team": team}}},
	}
	data, err := json.Marshal(Redact(w))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "canary-secret") {
		t.Fatalf("a Runtime Env value survived:\n%s", data)
	}
	if n := strings.Count(string(data), Redacted); n != 5 {
		t.Fatalf("(redacted) appears %d times, want 5 (one per Runtime copy: Sessions, ByName, Ptr, Any, Anon):\n%s", n, data)
	}
}

// A value with nothing to redact comes back equal, and nil stays nil.
func TestRedactLeavesPlainValuesAlone(t *testing.T) {
	if got := Redact(map[string]string{"status": "ok"}); got["status"] != "ok" {
		t.Fatalf("plain map changed: %v", got)
	}
	if got := Redact[*Session](nil); got != nil {
		t.Fatalf("nil pointer became %v", got)
	}
	var rt Runtime
	if got := Redact(rt); got.Env != nil {
		t.Fatalf("nil Env became %v", got.Env)
	}
}
