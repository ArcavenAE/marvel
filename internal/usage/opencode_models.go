package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// OpenCodeModelsPath returns where opencode keeps its model catalog:
// <cache>/opencode/models.json, with <cache> the absolute XDG_CACHE_HOME or
// else <home>/.cache. A relative XDG_CACHE_HOME is ignored, as the XDG base
// directory convention says. It returns "" when neither is available, which
// OpenCodeWindow reads as no catalog.
//
// getenv and home are parameters so a test can name a scratch home without
// touching the process environment.
func OpenCodeModelsPath(getenv func(string) string, home string) string {
	if x := getenv("XDG_CACHE_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "opencode", "models.json")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "opencode", "models.json")
}

// openCodeCatalog is the part of opencode's models.json this package reads:
// provider id -> models -> model id -> limit.context.
type openCodeCatalog map[string]struct {
	Models map[string]struct {
		Limit struct {
			Context float64 `json:"context"`
		} `json:"limit"`
	} `json:"models"`
}

// OpenCodeWindow reads limit.context for a launch model of the form
// "provider/model" from the opencode model catalog at path. The model id
// splits at the FIRST slash, so "openrouter/anthropic/claude-sonnet-4" is
// provider "openrouter", model "anthropic/claude-sonnet-4".
//
// Every failure returns 0 and an error: a missing file, a file that does not
// parse, a provider or model that is not in it, and a limit that is absent,
// zero, negative, fractional or past int range. The caller leaves the window
// unresolved on any error, so a bad catalog never becomes a window of 0 and
// never a guess. A missing file wraps os.ErrNotExist, so a caller can tell
// "opencode has not written one" from "the one it wrote is bad".
//
// The catalog file is the harness's own record and is read, never written: this
// package takes no lock on it and does not refresh it.
func OpenCodeWindow(path, providerModel string) (int, error) {
	provider, model, ok := strings.Cut(providerModel, "/")
	if !ok || provider == "" || model == "" {
		return 0, fmt.Errorf("opencode model %q is not provider/model", providerModel)
	}
	if path == "" {
		return 0, errors.New("no opencode model catalog path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read opencode model catalog: %w", err)
	}
	var catalog openCodeCatalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return 0, fmt.Errorf("parse opencode model catalog %s: %w", path, err)
	}
	m, ok := catalog[provider].Models[model]
	if !ok {
		return 0, fmt.Errorf("opencode model catalog has no %s/%s", provider, model)
	}
	c := m.Limit.Context
	if c < 1 || c != math.Trunc(c) || c > math.MaxInt32 {
		return 0, fmt.Errorf("opencode model catalog gives %s/%s no usable limit.context (%v)", provider, model, c)
	}
	return int(c), nil
}
