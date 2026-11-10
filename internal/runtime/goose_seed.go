package runtime

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/api"
)

// gooseManageExtensionsTool is the tool every seat has under never_allow: a seat
// that can add extensions can add a shell, so the permission file never leaves
// it out (goose-adapter-v1.md section 3, v1.1 fact L6).
const gooseManageExtensionsTool = "extensionmanager__manage_extensions"

// gooseConfigFile and goosePermissionFile are goose's own names under its config
// directory, which GOOSE_PATH_ROOT puts at <root>/config.
const (
	gooseConfigDir      = "config"
	gooseConfigFile     = "config.yaml"
	goosePermissionFile = "permission.yaml"
)

// goosePermission is permission.yaml as goose reads it at v1.57.0: a map from a
// permission category to three lists, every list required
// (crates/goose/src/config/permission.rs, PermissionConfig). approve reads the
// "user" category.
type goosePermission struct {
	AlwaysAllow []string `yaml:"always_allow"`
	AskBefore   []string `yaml:"ask_before"`
	NeverAllow  []string `yaml:"never_allow"`
}

// gooseExtensionEntry is a builtin extension entry in config.yaml, the smallest
// form goose accepts (ExtensionEntry over ExtensionConfig::Builtin, v1.57.0).
type gooseExtensionEntry struct {
	Enabled bool   `yaml:"enabled"`
	Type    string `yaml:"type"`
	Name    string `yaml:"name"`
}

// gooseTempName is the directory under the seat's home that holds its temp
// files when a sandbox profile is in force. It persists across launches and is
// never cleared.
const gooseTempName = "tmp"

// gooseTempDir is where a goose seat's temp files go under a sandbox profile:
// the shipped profile lets a seat write only to its private home and its working
// directory, so goose's temp file under the daemon's inherited per-user temp
// directory is refused (marvel#1315). It names a directory only when a profile
// is in force and the home is absolute, and never where the role set TMPDIR
// itself. The inherited process TMPDIR is not the role's: it is the value to
// replace.
//
// home is the seat's private home. The launch passes ctx.HarnessHomePath; the
// seeder passes the directory it is handed, because the manager calls Seed
// before it sets HarnessHomePath.
func gooseTempDir(ctx *LaunchContext, home string) (string, bool) {
	if ctx == nil || ctx.Session == nil || ctx.SandboxProfile == "" || !filepath.IsAbs(home) {
		return "", false
	}
	if _, set := ctx.Session.Runtime.Env["TMPDIR"]; set {
		return "", false
	}
	return filepath.Join(home, gooseTempName), true
}

// gooseSeeder returns the Seed step for one launch: config.yaml with the mode,
// the provider and model the role's env names, and the extensions; and
// permission.yaml with the role's tools. Neither carries a credential: the
// provider key stays where goose finds it (v1 section 6).
func gooseSeeder(ctx *LaunchContext) func(dir string) error {
	if ctx == nil || ctx.Session == nil {
		return nil
	}
	rt := ctx.Session.Runtime
	return func(dir string) error {
		g := rt.Goose
		if g == nil || g.Posture == "" {
			// A seat with no posture is refused, so it needs no seed.
			return nil
		}
		cfgDir := filepath.Join(dir, gooseConfigDir)
		if err := ensurePlainDir(cfgDir); err != nil {
			return fmt.Errorf("make goose config dir: %w", err)
		}
		cfg := map[string]any{gooseModeEnv: g.Posture}
		for _, k := range []string{"GOOSE_PROVIDER", "GOOSE_MODEL"} {
			if v := strings.TrimSpace(rt.Env[k]); v != "" {
				cfg[k] = v
			}
		}
		if len(g.Extensions) > 0 {
			exts := make(map[string]gooseExtensionEntry, len(g.Extensions))
			for _, name := range g.Extensions {
				exts[name] = gooseExtensionEntry{Enabled: true, Type: "builtin", Name: name}
			}
			cfg["extensions"] = exts
		}
		perm := map[string]goosePermission{"user": {
			AlwaysAllow: dedupe(g.ToolsAllow),
			AskBefore:   []string{},
			NeverAllow:  dedupe(append(slices.Clone(g.ToolsDeny), gooseManageExtensionsTool)),
		}}
		if err := writeGooseYAML(filepath.Join(cfgDir, gooseConfigFile), cfg); err != nil {
			return err
		}
		if err := writeGooseYAML(filepath.Join(cfgDir, goosePermissionFile), perm); err != nil {
			return err
		}
		// Last, so a failure here never costs the permission file a first
		// launch needs.
		if tmp, ok := gooseTempDir(ctx, dir); ok {
			if err := ensurePlainDir(tmp); err != nil {
				return fmt.Errorf("make goose temp dir: %w", err)
			}
		}
		return nil
	}
}

// dedupe keeps the first of each entry, in order, and returns an empty list
// rather than nil so the YAML key is a list, which goose requires.
func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// ensurePlainDir makes dir if it is absent and refuses anything present that is
// not a plain directory. The seat's kept home may hold a link planted between
// launches, and MkdirAll would follow it and put the seed outside the home.
func ensurePlainDir(dir string) error {
	fi, err := os.Lstat(dir)
	switch {
	case err == nil:
		if !fi.IsDir() {
			return fmt.Errorf("%s is not a plain directory (%s)", dir, fi.Mode().Type())
		}
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return os.Mkdir(dir, 0o700)
}

// writeGooseYAML writes one seed file in place. The open refuses a link at the
// path and does not block on a FIFO there (O_NONBLOCK; the type check below
// then refuses it), and the opened file must be a regular file with a single name before
// anything is truncated, so a hard link to a file outside the home is refused
// and not emptied.
func writeGooseYAML(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return fmt.Errorf("check %s: %w", filepath.Base(path), err)
	}
	if st, ok := opened.Sys().(*syscall.Stat_t); !opened.Mode().IsRegular() || !ok || uint64(st.Nlink) != 1 {
		return fmt.Errorf("check %s: not a regular file with one link", filepath.Base(path))
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	// Read it back by metadata: a path that took the write but is not the
	// plain file of the size written is not a seed goose will read.
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("check %s: %w", filepath.Base(path), err)
	}
	if !fi.Mode().IsRegular() || fi.Size() != int64(len(data)) {
		return fmt.Errorf("check %s: not a regular file of %d bytes", filepath.Base(path), len(data))
	}
	return nil
}

// gooseBuiltins is the --with-builtin argument for the role's extensions, or ""
// for none.
func gooseBuiltins(g *api.GooseRuntime) string {
	if g == nil {
		return ""
	}
	return strings.Join(g.Extensions, ",")
}
