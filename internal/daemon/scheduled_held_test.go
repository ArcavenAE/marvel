package daemon

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// describe team says why a stored scheduled role is held, and a re-apply of a
// valid manifest clears the mark (aae-orc-iyun3 part c).

const heldManifest = `
[workspace]
name = "hheld"

[[team]]
name = "crew"

  [[team.role]]
  name = "job"
  replicas = 1
  permissions = "dontAsk"

    [team.role.runtime]
    image = "sleep"
    command = "sleep"
    args = ["300"]
    mode = "headless"
    prompt = "go"

    [team.role.schedule]
    cron = "0 6 * * *"
    timezone = "Etc/UTC"
    active_deadline = "30m"
    stale_after = "48h"
`

func TestDescribeTeamSaysWhyARoleIsHeldAndReapplyClearsIt(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, heldManifest); resp.Error != "" {
		t.Fatalf("setup apply: %s", resp.Error)
	}
	reason := "refused now: this launch skips permission checks"
	if err := d.store.UpdateTeam("hheld/crew", func(live *api.Team) error {
		live.Roles[0].Held = reason
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	desc, err := d.describeTeam("hheld/crew")
	if err != nil {
		t.Fatal(err)
	}
	if !hasCondition(desc, "ScheduleRefused", "job", reason) {
		t.Fatalf("conditions = %+v, want ScheduleRefused for job naming the reason", desc.Conditions)
	}
	if resp := applyManifest(t, d, heldManifest); resp.Error != "" {
		t.Fatalf("re-apply: %s", resp.Error)
	}
	desc, err = d.describeTeam("hheld/crew")
	if err != nil {
		t.Fatal(err)
	}
	if hasCondition(desc, "ScheduleRefused", "job", "") {
		t.Errorf("conditions = %+v, want no ScheduleRefused after a re-apply of a valid manifest", desc.Conditions)
	}
}
