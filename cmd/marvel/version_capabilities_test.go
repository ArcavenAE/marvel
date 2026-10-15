package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/daemon"
)

// The departing daemon asks the binary it is about to exec whether it can read
// the credential handoff, before it creates anything. The answer is local: it
// never dials a daemon (aae-orc-4h9xp, section 1.1).
func TestVersionCapabilitiesJSONAnswersLocallyWithTheHandoffFormat(t *testing.T) {
	t.Setenv("MARVEL_SOCKET", "/nonexistent/marvel.sock")
	var out bytes.Buffer
	cmd := versionCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--capabilities", "--json"})
	start := time.Now()
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("the capability answer took %s; it must not wait on a daemon", time.Since(start))
	}
	var got map[string]int
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("the output is not one JSON object: %q (%v)", out.String(), err)
	}
	if got["reexec_handoff"] != daemon.ReexecHandoffFormat || daemon.ReexecHandoffFormat != 1 {
		t.Errorf("reexec_handoff = %d, want the daemon's format %d (1)", got["reexec_handoff"], daemon.ReexecHandoffFormat)
	}
	if want := fmt.Sprintf(`{"reexec_handoff":%d}`, daemon.ReexecHandoffFormat) + "\n"; out.String() != want {
		t.Errorf("output = %q, want exactly %q", out.String(), want)
	}
}

func TestPlainVersionStillPrintsTheClientLineFirst(t *testing.T) {
	cmd := versionCmd()
	if f := cmd.Flags().Lookup("capabilities"); f == nil || f.DefValue != "false" {
		t.Fatal("version has no --capabilities flag defaulting to false")
	}
	if f := cmd.Flags().Lookup("json"); f == nil {
		t.Fatal("version has no --json flag")
	}
}
