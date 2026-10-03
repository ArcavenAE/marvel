package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/upgrade"
)

// The daemon is told to re-exec only into a binary that changed on disk. An
// upgrade that installed nothing must not re-exec: the host would report
// "upgraded and re-executed" while still running the old build.
func TestAfterUpgradeReexecsOnlyIntoAChangedBinary(t *testing.T) {
	cases := []struct {
		name        string
		changed     bool
		flag        bool
		wantReexec  bool
		wantMessage string
	}{
		{"changed with --daemon", true, true, true, ""},
		{"unchanged with --daemon", false, true, false, "marvel daemon reexec"},
		{"changed without --daemon", true, false, false, ""},
		{"unchanged without --daemon", false, false, false, ""},
	}
	for _, tc := range cases {
		calls := 0
		var buf bytes.Buffer
		err := afterUpgrade(upgrade.Result{Changed: tc.changed}, tc.flag, func() error { calls++; return nil }, &buf)
		if err != nil {
			t.Errorf("%s: error = %v", tc.name, err)
		}
		if (calls == 1) != tc.wantReexec || calls > 1 {
			t.Errorf("%s: re-exec called %d times, want called=%v", tc.name, calls, tc.wantReexec)
		}
		if tc.wantMessage != "" && !strings.Contains(buf.String(), tc.wantMessage) {
			t.Errorf("%s: output %q does not mention %q", tc.name, buf.String(), tc.wantMessage)
		}
		if tc.wantMessage != "" && !strings.Contains(buf.String(), "not re-executed") {
			t.Errorf("%s: output %q does not say the daemon was not re-executed", tc.name, buf.String())
		}
	}
}

func TestAfterUpgradeReportsAFailedReexec(t *testing.T) {
	err := afterUpgrade(upgrade.Result{Changed: true}, true, func() error { return errors.New("socket closed") }, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "binary upgraded, but") || !strings.Contains(err.Error(), "socket closed") {
		t.Errorf("error = %v, want binary upgraded, but the re-exec failed", err)
	}
}
