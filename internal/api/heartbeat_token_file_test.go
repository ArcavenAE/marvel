package api

import (
	"os"
	"path/filepath"
	"strings"
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

// A launch sets one delivery or the other, so both set means a stale variable
// inherited from a parent; the file is the one marvel wrote for this seat.
func TestHeartbeatTokenFromEnvPrefersTheFileOverTheVariable(t *testing.T) {
	t.Setenv(HeartbeatTokenEnv, "stale-from-env")
	t.Setenv(HeartbeatTokenFileEnv, tokenFile(t, "from-file"))
	if got := HeartbeatTokenFromEnv(); got != "from-file" {
		t.Errorf("token = %q, want from-file", got)
	}
}

func TestHeartbeatTokenFromEnvDoesNotFallBackToTheVariableWhenTheNamedFileCannotBeRead(t *testing.T) {
	t.Setenv(HeartbeatTokenEnv, "stale-from-env")
	t.Setenv(HeartbeatTokenFileEnv, filepath.Join(t.TempDir(), "absent"))
	if got := HeartbeatTokenFromEnv(); got != "" {
		t.Errorf("token = %q, want empty: a named file that cannot be read is not licence to use a stale variable", got)
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

// A forwarder that reads the variable itself honors one delivery and misses
// the other, which is the failure HeartbeatTokenFromEnv exists to remove.
func TestNoForwarderReadsTheTokenVariableDirectly(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	reads := []string{"os.Getenv(api.HeartbeatTokenEnv)", "os.Getenv(HeartbeatTokenEnv)", `os.Getenv("MARVEL_HEARTBEAT_TOKEN")`, `os.LookupEnv(api.HeartbeatTokenEnv)`, `os.LookupEnv(HeartbeatTokenEnv)`}
	var seen int
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "vendor") {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		seen++
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if filepath.Base(path) == "heartbeat.go" && filepath.Base(filepath.Dir(path)) == "api" {
			return nil
		}
		for _, r := range reads {
			if strings.Contains(string(src), r) {
				t.Errorf("%s reads the token variable with %s; use api.HeartbeatTokenFromEnv", path, r)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 50 {
		t.Fatalf("scanned %d Go files under %s; the walk is not reaching the tree", seen, root)
	}
}
