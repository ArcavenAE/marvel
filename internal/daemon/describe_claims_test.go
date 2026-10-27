package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// describe team prints the team's claims: the sources that applied it, and who
// released the last one (docs/design/work-prune.md section 3.2).
func TestDescribeTeamPrintsTheClaims(t *testing.T) {
	at := time.Date(2026, 10, 27, 22, 0, 0, 0, time.UTC)
	team := api.Team{Name: "alpha", Workspace: "ws1", Claims: &api.TeamClaims{
		Sources:    map[string]time.Time{"host-a:/work/a/marvel.toml": at},
		ReleasedBy: "host-b:/work/b/marvel.toml",
		ReleasedAt: at,
	}}
	out, err := json.Marshal(teamDescription{Team: team})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"Claims":`, `"host-a:/work/a/marvel.toml":"2026-10-27T22:00:00Z"`, `"ReleasedBy":"host-b:/work/b/marvel.toml"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("describe team output lacks %s:\n%s", want, out)
		}
	}
}
