package api

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHibernateRolesAfter(t *testing.T) {
	cases := []struct {
		name string
		prev *HibernateMark
		add  []string
		want []string
	}{
		{"first subset", nil, []string{"b", "a"}, []string{"a", "b"}},
		{"second subset adds", &HibernateMark{Roles: []string{"a"}}, []string{"c", "a"}, []string{"a", "c"}},
		{"whole team empties the list", &HibernateMark{Roles: []string{"a"}}, nil, nil},
		{"a whole-team mark stays whole", &HibernateMark{}, []string{"a"}, nil},
	}
	for _, c := range cases {
		if got := HibernateRolesAfter(c.prev, c.add); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: roles = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHibernateMarkCovers(t *testing.T) {
	var none *HibernateMark
	if none.Covers("a") {
		t.Error("a nil mark covers a role")
	}
	if !(&HibernateMark{}).Covers("a") {
		t.Error("a whole-team mark does not cover a role")
	}
	m := &HibernateMark{Roles: []string{"a"}}
	if !m.Covers("a") || m.Covers("b") {
		t.Error("a subset mark must cover exactly its roles")
	}
}

func TestCloneTeamSharesNoHibernateState(t *testing.T) {
	team := &Team{
		Name: "t", Workspace: "w",
		Hibernate:         &HibernateMark{State: HibernateHibernated, Roles: []string{"a"}},
		HibernateRequests: map[string]ShiftRequest{"w/s1": {Session: "w/s1"}},
	}
	c := cloneTeam(team)
	c.Hibernate.Roles[0] = "z"
	delete(c.HibernateRequests, "w/s1")
	if team.Hibernate.Roles[0] != "a" || len(team.HibernateRequests) != 1 {
		t.Fatalf("the clone aliases the original: %+v", team)
	}
}

const hibernateApplyManifest = `
[workspace]
name = "acme"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
`

// apply never sets or clears the mark: a re-apply during a hibernate keeps it,
// and keeps the open requests.
func TestApplyKeepsTheHibernateMarkAndRequests(t *testing.T) {
	store := NewStore()
	m, err := ParseManifestBytes([]byte(hibernateApplyManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(store); err != nil {
		t.Fatal(err)
	}
	mark := &HibernateMark{State: HibernateHibernated, Since: time.Now().UTC(), Actor: "op", Source: "cli", Mode: HibernateModeHandoff}
	if err := store.UpdateTeam("acme/squad", func(live *Team) error {
		live.Hibernate = mark
		live.HibernateRequests = map[string]ShiftRequest{"acme/squad-worker-g1-0": {Session: "acme/squad-worker-g1-0", Cause: HandoffCauseHibernate}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	m2, err := ParseManifestBytes([]byte(hibernateApplyManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := m2.Apply(store); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTeam("acme/squad")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hibernate == nil || got.Hibernate.State != HibernateHibernated || got.Hibernate.Actor != "op" {
		t.Fatalf("mark after a re-apply = %+v, want it kept", got.Hibernate)
	}
	if len(got.HibernateRequests) != 1 {
		t.Fatalf("requests after a re-apply = %v, want the open one kept", got.HibernateRequests)
	}
}

// The mark and its requests are stored with the team, so a restart finds them.
func TestHibernateStateSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTeam(&Team{Name: "squad", Workspace: "acme", Generation: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTeam("acme/squad", func(live *Team) error {
		live.Hibernate = &HibernateMark{State: HibernateWaking, Roles: []string{"a"}}
		live.HibernateRequests = map[string]ShiftRequest{"acme/x": {Session: "acme/x", Cause: HandoffCauseHibernate}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.CloseBolt() }()
	got, err := s2.GetTeam("acme/squad")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hibernate == nil || got.Hibernate.State != HibernateWaking || !reflect.DeepEqual(got.Hibernate.Roles, []string{"a"}) ||
		got.HibernateRequests["acme/x"].Cause != HandoffCauseHibernate {
		t.Fatalf("team after reopen = %+v", got)
	}
}

// L1: "none" names an unlabeled role in a selector, so it cannot be a label.
func TestManifestRefusesNoneAsABackendLabel(t *testing.T) {
	_, err := ParseManifestBytes([]byte(strings.Replace(hibernateApplyManifest,
		`command = "sleep"`, "command = \"sleep\"\n    backend = \"none\"", 1)))
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("a role declaring backend none parsed: err = %v", err)
	}
	if _, err := ParseManifestBytes([]byte(strings.Replace(hibernateApplyManifest,
		`command = "sleep"`, "command = \"sleep\"\n    backend = \"bedrock\"", 1))); err != nil {
		t.Fatalf("a real label was refused: %v", err)
	}
}
