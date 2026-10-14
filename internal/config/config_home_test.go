package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

// Not parallel: these set HOME and MARVEL_HOME.

func TestConfigIsReadFromTheMarvelHomeRoot(t *testing.T) {
	user := t.TempDir()
	root := filepath.Join(t.TempDir(), "mh")
	t.Setenv("HOME", user)
	t.Setenv(paths.HomeEnv, root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	// A config under the user's real home must not be the one that is read.
	if err := os.MkdirAll(filepath.Join(user, ".marvel"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(user, ".marvel", "config.yaml"), []byte("clusters:\n  - name: home\ncurrent_cluster: home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("clusters:\n  - name: rooted\ncurrent_cluster: rooted\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.CurrentCluster != "rooted" {
		t.Errorf("current cluster = %q, want %q: Load did not read <root>/config.yaml", cfg.CurrentCluster, "rooted")
	}
}

func TestSaveWritesTheConfigUnderTheMarvelHomeRoot(t *testing.T) {
	user := t.TempDir()
	root := filepath.Join(t.TempDir(), "mh")
	t.Setenv("HOME", user)
	t.Setenv(paths.HomeEnv, root)

	if err := Save(&Config{Clusters: []Cluster{{Name: "rooted"}}, CurrentCluster: "rooted"}); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); err != nil {
		t.Errorf("config.yaml is not under the root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(user, ".marvel")); err == nil {
		t.Errorf("Save wrote under the user's home although a root was named")
	}
}

func TestConfigPathUnsetIsTheDefaultLayout(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv(paths.HomeEnv, "")
	got, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(user, ".marvel", "config.yaml"); got != want {
		t.Errorf("configPath() = %q, want %q", got, want)
	}
}

func TestTheDefaultSocketIsUnderTheMarvelHomeRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mh")
	t.Setenv(SocketEnv, "")
	t.Setenv(paths.HomeEnv, root)
	if got, want := ResolveSocket(), filepath.Join(root, "run", "marvel.sock"); got != want {
		t.Errorf("ResolveSocket() = %q, want %q", got, want)
	}
}
