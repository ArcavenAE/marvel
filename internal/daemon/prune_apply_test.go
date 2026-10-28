package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
)

// pruneApplyDaemon is a daemon whose store holds ws/gone, claimed only by
// pruneSrc and not named by pruneManifest, with one finished seat; seed adds
// more teams before the apply.
func pruneApplyDaemon(t *testing.T, seed func(s *api.Store, at time.Time)) *Daemon {
	t.Helper()
	d := newHandlerDaemon(t)
	at := time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)
	if err := d.store.CreateWorkspace(&api.Workspace{Name: "ws", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	addPruneTeam(t, d.store, api.Team{Name: "gone", Claims: claimed(at, pruneSrc)})
	addPruneSession(t, d.store, api.Session{Name: "gone-worker-g1-0", Team: "gone", Role: "worker", Generation: 1, State: api.SessionFailed})
	if seed != nil {
		seed(d.store, at)
	}
	return d
}

func claimed(at time.Time, sources ...string) *api.TeamClaims {
	c := &api.TeamClaims{Sources: map[string]time.Time{}}
	for _, s := range sources {
		c.Sources[s] = at
	}
	return c
}

func addPruneTeam(t *testing.T, s *api.Store, tm api.Team) {
	t.Helper()
	tm.Workspace = "ws"
	if len(tm.Roles) == 0 {
		tm.Roles = []api.Role{{Name: "worker"}}
	}
	if err := s.CreateTeam(&tm); err != nil {
		t.Fatal(err)
	}
}

func addPruneSession(t *testing.T, s *api.Store, sess api.Session) {
	t.Helper()
	sess.Workspace = "ws"
	if err := s.CreateSession(&sess); err != nil {
		t.Fatal(err)
	}
}

// previewDigest is the digest marvel plan -f --prune shows for pruneManifest.
func previewDigest(t *testing.T, d *Daemon) string {
	t.Helper()
	resp := prunePlanRequest(t, d, pruneSrc)
	if resp.Error != "" {
		t.Fatalf("prune_plan: %s", resp.Error)
	}
	var plan api.PrunePlan
	if err := json.Unmarshal(resp.Result, &plan); err != nil {
		t.Fatal(err)
	}
	return plan.Digest
}

func applyPrune(t *testing.T, d *Daemon, source, digest string) daemonrpc.Response {
	t.Helper()
	p := map[string]any{"manifest_data": []byte(pruneManifest), "prune": true, "prune_digest": digest}
	if source != "" {
		p["manifest_source"] = source
	}
	params, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return d.handleApply(params)
}

type pruneReply struct {
	Pruned  []string           `json:"pruned"`
	Plan    api.PrunePlan      `json:"prune_plan"`
	Given   string             `json:"prune_digest_given"`
	Refused string             `json:"prune_refused"`
	Skipped []api.PruneSkipped `json:"prune_skipped"`
}

func decodePrune(t *testing.T, resp daemonrpc.Response) (pruneReply, map[string]json.RawMessage) {
	t.Helper()
	if resp.Error != "" {
		t.Fatalf("apply --prune: %s", resp.Error)
	}
	var r pruneReply
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(resp.Result, &raw); err != nil {
		t.Fatal(err)
	}
	return r, raw
}

func teamExists(d *Daemon, key string) bool {
	_, err := d.store.GetTeam(key)
	return err == nil
}

// The digest the preview printed prunes the set: the apply stands, ws/gone and
// its session are deleted, and team.pruned records it (section 5, Q11).
func TestApplyPruneDeletesThePreviewedSet(t *testing.T) {
	d := pruneApplyDaemon(t, nil)
	digest := previewDigest(t, d)

	r, _ := decodePrune(t, applyPrune(t, d, pruneSrc, digest))
	if strings.Join(r.Pruned, ",") != "ws/gone" || r.Refused != "" {
		t.Fatalf("pruned %q, refused %q; want ws/gone pruned", r.Pruned, r.Refused)
	}
	if teamExists(d, "ws/gone") || len(d.store.ListSessionsByTeam("ws", "gone")) != 0 {
		t.Error("ws/gone or its session is still stored")
	}
	if !teamExists(d, "ws/kept") {
		t.Error("the apply did not stand: ws/kept is missing")
	}
	evs := d.events.Snapshot(events.Filter{Kind: events.KindTeamPruned}, 0)
	if len(evs) != 1 || evs[0].Team != "gone" || !strings.Contains(evs[0].Message, pruneSrc) {
		t.Errorf("team.pruned events = %+v, want one for gone naming the source", evs)
	}
}

// A well-formed digest that is not the current set's applies the manifest and
// prunes nothing, and the response carries the current set and both digests
// (section 5 item 2, rulings Q6, Q7).
func TestApplyPruneMismatchAppliesAndPrunesNothing(t *testing.T) {
	d := pruneApplyDaemon(t, nil)
	digest := previewDigest(t, d)

	r, raw := decodePrune(t, applyPrune(t, d, pruneSrc, "ffffffff"))
	if string(raw["pruned"]) != "[]" || string(raw["draining"]) != "[]" {
		t.Errorf("pruned = %s, draining = %s; want both []", raw["pruned"], raw["draining"])
	}
	if r.Refused == "" || r.Given != "ffffffff" || r.Plan.Digest != digest {
		t.Errorf("refused %q, given %q, current digest %q; want a refusal with ffffffff and %s", r.Refused, r.Given, r.Plan.Digest, digest)
	}
	if len(r.Plan.WouldDelete) != 1 || r.Plan.WouldDelete[0].Team != "ws/gone" {
		t.Errorf("current set = %+v, want ws/gone", r.Plan.WouldDelete)
	}
	if !teamExists(d, "ws/gone") || !teamExists(d, "ws/kept") {
		t.Error("want ws/gone kept and ws/kept applied")
	}
}

// A malformed digest or a missing source is refused before anything is
// applied, so the file's teams are not even created (rulings Q5, Q7).
func TestApplyPruneRefusesBeforeTheApply(t *testing.T) {
	for _, tc := range []struct {
		name, source, digest, want string
	}{
		{"malformed digest", pruneSrc, "FFFFFFFF", "nothing was applied"},
		{"short digest", pruneSrc, "abc", "nothing was applied"},
		{"no source", "", "0123abcd", "manifest source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := pruneApplyDaemon(t, nil)
			resp := applyPrune(t, d, tc.source, tc.digest)
			if resp.Error == "" || !strings.Contains(resp.Error, tc.want) {
				t.Fatalf("apply = %q, %s; want a refusal with %q", resp.Error, resp.Result, tc.want)
			}
			if teamExists(d, "ws/kept") || !teamExists(d, "ws/gone") {
				t.Error("the refused prune applied or deleted something")
			}
		})
	}
}

// An empty prune set still answers pruned as an array, never null or omitted,
// since the client tells an older daemon by that key (ruling Q12).
func TestApplyPruneAnswersAnEmptyArray(t *testing.T) {
	d := newHandlerDaemon(t)
	digest := previewDigest(t, d)
	r, raw := decodePrune(t, applyPrune(t, d, pruneSrc, digest))
	if string(raw["pruned"]) != "[]" || len(r.Pruned) != 0 {
		t.Errorf("pruned = %s, want []", raw["pruned"])
	}
	if string(raw["draining"]) != "[]" {
		t.Errorf("draining = %s, want []", raw["draining"])
	}
}

// A team with a live interactive seat is marked and left to drain, not
// deleted in the request: pruned keeps step 3's meaning, the teams deleted
// now, and draining lists the marked team with its seat count (step 4
// ruling, Q6).
func TestApplyPruneListsALiveTeamAsDraining(t *testing.T) {
	d := pruneApplyDaemon(t, func(s *api.Store, at time.Time) {
		addPruneTeam(t, s, api.Team{Name: "two", Claims: claimed(at, pruneSrc)})
		addPruneSession(t, s, api.Session{Name: "two-worker-g1-0", Team: "two", Role: "worker", Generation: 1, State: api.SessionRunning})
	})
	digest := previewDigest(t, d)
	resp := applyPrune(t, d, pruneSrc, digest)
	var reply struct {
		Pruned   []string            `json:"pruned"`
		Draining []api.PruneDraining `json:"draining"`
	}
	if resp.Error != "" {
		t.Fatalf("apply --prune: %s", resp.Error)
	}
	if err := json.Unmarshal(resp.Result, &reply); err != nil {
		t.Fatal(err)
	}
	if strings.Join(reply.Pruned, ",") != "ws/gone" {
		t.Errorf("pruned %q, want only ws/gone, deleted in the request", reply.Pruned)
	}
	if len(reply.Draining) != 1 || reply.Draining[0].Team != "ws/two" || reply.Draining[0].Seats != 1 {
		t.Errorf("draining = %+v, want ws/two with 1 seat", reply.Draining)
	}
}

// The teams the ticket names are kept or deferred: one claimed by two sources,
// one with a marvel run session, one mid-shift, and one with a live headless
// seat (section 5; step 4 ruling Q7). A live interactive seat no longer defers
// its team: TestApplyPruneListsALiveTeamAsDraining covers it. The apply's own reconcile reaps a seeded
// session that has no pane, so that the run session survives the prune is
// checked in the controller's TestPruneLeavesARunSessionAndItsTeam.
func TestApplyPruneKeepsWhatIsNotThisFilesToDelete(t *testing.T) {
	headless := api.Runtime{Mode: api.RuntimeModeHeadless}
	for _, tc := range []struct {
		name    string
		team    func(at time.Time) api.Team
		session *api.Session
	}{
		{"claimed by two sources", func(at time.Time) api.Team {
			return api.Team{Name: "two", Claims: claimed(at, pruneSrc, "host-a:/srv/manifests/ops.toml")}
		}, nil},
		{
			"a run session", func(at time.Time) api.Team { return api.Team{Name: "two", Claims: claimed(at, pruneSrc)} },
			&api.Session{Name: "two-run", Team: "two", Role: "worker", Generation: 0, State: api.SessionRunning},
		},
		{"mid-shift", func(at time.Time) api.Team {
			return api.Team{Name: "two", Claims: claimed(at, pruneSrc), Shift: api.ShiftState{Phase: api.ShiftDraining}}
		}, nil},
		{"a live headless seat", func(at time.Time) api.Team {
			return api.Team{Name: "two", Claims: claimed(at, pruneSrc), Roles: []api.Role{{Name: "batch", Runtime: headless}}}
		}, &api.Session{Name: "two-batch-g1-0", Team: "two", Role: "batch", Generation: 1, State: api.SessionRunning}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := pruneApplyDaemon(t, func(s *api.Store, at time.Time) {
				addPruneTeam(t, s, tc.team(at))
				if tc.session != nil {
					addPruneSession(t, s, *tc.session)
				}
			})
			digest := previewDigest(t, d)
			r, _ := decodePrune(t, applyPrune(t, d, pruneSrc, digest))
			if strings.Join(r.Pruned, ",") != "ws/gone" {
				t.Errorf("pruned %q, want only ws/gone", r.Pruned)
			}
			got, err := d.store.GetTeam("ws/two")
			if err != nil {
				t.Fatalf("ws/two was deleted: %v", err)
			}
			if got.Pruning != nil {
				t.Errorf("ws/two was marked: %+v", got.Pruning)
			}
		})
	}
}

// marvel run into a team being pruned is refused, so no run session lands in
// a team about to be deleted (the director's acceptance item, ruling Q8).
func TestRunIsRefusedIntoAPruningTeam(t *testing.T) {
	d := pruneApplyDaemon(t, nil)
	if err := d.store.UpdateTeam("ws/gone", func(live *api.Team) error {
		live.Pruning = &api.TeamPruning{Source: pruneSrc}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	params, err := json.Marshal(map[string]any{"workspace": "ws", "team": "gone", "role": "worker", "runtime_command": "sleep", "runtime_args": []string{"300"}})
	if err != nil {
		t.Fatal(err)
	}
	resp := d.handleRun(params)
	if resp.Error == "" || !strings.Contains(resp.Error, "being pruned") {
		t.Fatalf("run into a pruning team = %q, %s; want a refusal", resp.Error, resp.Result)
	}
	if n := len(d.store.ListSessionsByTeam("ws", "gone")); n != 1 {
		t.Errorf("sessions in ws/gone = %d, want the one finished seat only", n)
	}
}
