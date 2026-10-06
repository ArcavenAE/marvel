package panestate

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed patterns
var embedded embed.FS

type patternFile struct {
	ID             string   `yaml:"id"`
	Version        int      `yaml:"version"`
	Harness        string   `yaml:"harness"`
	HarnessVersion string   `yaml:"harness_version"`
	SampleWidth    int      `yaml:"sample_width"`
	VarRunes       int      `yaml:"var_runes"`
	Rows           []string `yaml:"rows"`
}

// Load reads every <harness>/<harness version>/<id>.yaml under root.
func Load(fsys fs.FS, root string) ([]Pattern, error) {
	var out []Pattern
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".yaml") {
			return nil
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		var f patternFile
		if err := yaml.Unmarshal(raw, &f); err != nil {
			return fmt.Errorf("pattern %s: %w", p, err)
		}
		if f.ID == "" || f.Harness == "" || f.HarnessVersion == "" || len(f.Rows) == 0 {
			return fmt.Errorf("pattern %s: id, harness, harness_version and rows are required", p)
		}
		if f.VarRunes < 0 {
			return fmt.Errorf("pattern %s: var_runes must not be negative", p)
		}
		pat := Pattern{ID: f.ID, Version: f.Version, Harness: f.Harness, HarnessVersion: f.HarnessVersion, SampleWidth: f.SampleWidth}
		for _, r := range f.Rows {
			row, err := parseRow(r)
			if err != nil {
				return fmt.Errorf("pattern %s: %w", p, err)
			}
			row.MaxVar = f.VarRunes
			pat.Rows = append(pat.Rows, row)
		}
		out = append(out, pat)
		return nil
	})
	return out, err
}

// Sample returns the captured screen a pattern was built from, stored beside
// it as <id>.sample.txt.
func Sample(fsys fs.FS, root string, p Pattern) (string, error) {
	raw, err := fs.ReadFile(fsys, path.Join(root, p.Harness, p.HarnessVersion, p.ID+".sample.txt"))
	return string(raw), err
}

// LoadEmbedded loads the pattern sets shipped with the binary.
func LoadEmbedded() ([]Pattern, error) {
	return Load(embedded, "patterns")
}

// EmbeddedSample is Sample over the shipped sets.
func EmbeddedSample(p Pattern) (string, error) {
	return Sample(embedded, "patterns", p)
}
