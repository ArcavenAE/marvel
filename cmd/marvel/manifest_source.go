package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// manifestSource names where a manifest is applied from, "<host>:<path>": the
// host name cut at the first dot and lowercased, and the manifest file's
// absolute path with symlinks resolved (docs/design/work-prune.md section 3.1).
// The daemon records it as the claim on every team the file names.
func manifestSource(manifestPath string) (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("host name: %w", err)
	}
	return sourceOf(host, manifestPath)
}

func sourceOf(hostname, manifestPath string) (string, error) {
	host, _, _ := strings.Cut(hostname, ".")
	host = strings.ToLower(host)
	if host == "" {
		return "", errors.New("host name is empty")
	}
	abs, err := filepath.Abs(manifestPath)
	if err != nil {
		return "", fmt.Errorf("absolute path of %s: %w", manifestPath, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", abs, err)
	}
	return host + ":" + resolved, nil
}
