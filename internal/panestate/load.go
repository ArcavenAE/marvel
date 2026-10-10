package panestate

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed patterns
var embedded embed.FS

type patternFile struct {
	ID             string `yaml:"id"`
	Version        int    `yaml:"version"`
	Harness        string `yaml:"harness"`
	HarnessVersion string `yaml:"harness_version"`
	VersionRange   *struct {
		Min string `yaml:"min"`
		Max string `yaml:"max"`
	} `yaml:"version_range"`
	State       string   `yaml:"state"`
	Reason      string   `yaml:"reason"`
	SampleWidth int      `yaml:"sample_width"`
	VarRunes    int      `yaml:"var_runes"`
	Rows        []string `yaml:"rows"`
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
		state, err := patternState(f.State, f.Reason)
		if err != nil {
			return fmt.Errorf("pattern %s: %w", p, err)
		}
		pat := Pattern{State: state, Reason: f.Reason, ID: f.ID, Version: f.Version, Harness: f.Harness, HarnessVersion: f.HarnessVersion, SampleWidth: f.SampleWidth}
		if r := f.VersionRange; r != nil {
			pat.MinVersion, pat.MaxVersion = r.Min, r.Max
			if err := checkRange(pat); err != nil {
				return fmt.Errorf("pattern %s: %w", p, err)
			}
		}
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

// checkRange refuses a version_range that is not a range of dotted numbers with
// both bounds, in order, around the version the sample was captured from.
func checkRange(p Pattern) error {
	lo, lok := parseVersion(p.MinVersion)
	hi, hok := parseVersion(p.MaxVersion)
	at, aok := parseVersion(p.HarnessVersion)
	switch {
	case !lok || !hok:
		return fmt.Errorf("version_range needs min and max, each dotted numbers")
	case !aok || len(lo) != len(hi) || len(lo) != len(at):
		return fmt.Errorf("version_range bounds and harness_version must have the same number of parts")
	case compareVersion(lo, hi) > 0:
		return fmt.Errorf("version_range min %s is above max %s", p.MinVersion, p.MaxVersion)
	case compareVersion(at, lo) < 0 || compareVersion(at, hi) > 0:
		return fmt.Errorf("harness_version %s, the sampled version, is outside version_range %s", p.HarnessVersion, p.VersionLabel())
	}
	return nil
}

// parkedReasons are the reasons a parked pattern may name.
var parkedReasons = []string{"trust", "permission", "update", "other"}

// patternState reads a pattern's state and reason. No state means logged-out,
// the only state a pattern set before parked existed. A parked pattern names
// one of the reasons, and no other state carries one.
func patternState(state, reason string) (State, error) {
	switch State(state) {
	case "", StateLoggedOut:
		if reason != "" {
			return "", fmt.Errorf("reason is only for a parked pattern")
		}
		return StateLoggedOut, nil
	case StateParked:
		if !slices.Contains(parkedReasons, reason) {
			return "", fmt.Errorf("a parked pattern needs a reason, one of %s", strings.Join(parkedReasons, ", "))
		}
		return StateParked, nil
	default:
		return "", fmt.Errorf("state must be logged-out or parked")
	}
}
