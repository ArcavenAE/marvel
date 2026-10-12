package api

import (
	"os"
	"path/filepath"
	"testing"
)

func tokenFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHeartbeatTokenFromEnvReadsTheTokenVariable(t *testing.T) {
	t.Setenv(HeartbeatTokenEnv, "from-env")
	t.Setenv(HeartbeatTokenFileEnv, "")
	if got := HeartbeatTokenFromEnv(); got != "from-env" {
		t.Errorf("token = %q, want from-env", got)
	}
}

func TestHeartbeatTokenFromEnvReadsTheFileWhenTheVariableIsEmpty(t *testing.T) {
	t.Setenv(HeartbeatTokenEnv, "")
	t.Setenv(HeartbeatTokenFileEnv, tokenFile(t, "from-file\n"))
	if got := HeartbeatTokenFromEnv(); got != "from-file" {
		t.Errorf("token = %q, want from-file with its newline trimmed", got)
	}
}

func TestHeartbeatTokenFromEnvPrefersTheVariableOverTheFile(t *testing.T) {
	t.Setenv(HeartbeatTokenEnv, "from-env")
	t.Setenv(HeartbeatTokenFileEnv, tokenFile(t, "from-file"))
	if got := HeartbeatTokenFromEnv(); got != "from-env" {
		t.Errorf("token = %q, want from-env", got)
	}
}

func TestHeartbeatTokenFromEnvIsEmptyWhenTheFileCannotBeRead(t *testing.T) {
	t.Setenv(HeartbeatTokenEnv, "")
	t.Setenv(HeartbeatTokenFileEnv, filepath.Join(t.TempDir(), "absent"))
	if got := HeartbeatTokenFromEnv(); got != "" {
		t.Errorf("token = %q from an absent file, want empty", got)
	}
	t.Setenv(HeartbeatTokenFileEnv, "")
	if got := HeartbeatTokenFromEnv(); got != "" {
		t.Errorf("token = %q with neither variable set, want empty", got)
	}
}

func TestNewHeartbeatRequestReadsTheTokenFromTheFile(t *testing.T) {
	t.Setenv(HeartbeatTokenEnv, "")
	t.Setenv(HeartbeatTokenFileEnv, tokenFile(t, "from-file"))
	req := NewHeartbeatRequest("acme/s", 10, "m")
	if req.SessionToken != "from-file" {
		t.Errorf("request token = %q, want from-file", req.SessionToken)
	}
}
