package daemon

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// The hibernate and wake RPCs (docs/design/fleet-hibernate.md sections 3, 4, 6
// and 7), local only like suspend and resume. Both answer at once: the sign-off
// and the wake run on the reconcile ticks, and a status call reads where they
// stand.

// Preflight check names, as --skip-check takes them.
const (
	checkDaemon  = "daemon"
	checkBus     = "bus"
	checkHarness = "harness-binary"
)

// hibernateParams is the request. Teams names the teams, All takes every team,
// Backend and ExceptBackend select roles by label, End names one seat to end
// without a handoff, DryRun counts what would be signed off, and Status reads
// where each team stands.
type hibernateParams struct {
	Teams         []string `json:"teams,omitempty"`
	All           bool     `json:"all,omitempty"`
	Backend       []string `json:"backend,omitempty"`
	ExceptBackend []string `json:"except_backend,omitempty"`
	End           string   `json:"end,omitempty"`
	DryRun        bool     `json:"dry_run,omitempty"`
	Status        bool     `json:"status,omitempty"`
}

// hibernateTeamStatus is where one team stands.
type hibernateTeamStatus struct {
	Team  string   `json:"team"`
	State string   `json:"state"`
	Since string   `json:"since,omitempty"`
	Actor string   `json:"actor,omitempty"`
	Roles []string `json:"roles,omitempty"`
	// Running is the covered seats still running; Pending the ones asked and
	// inside their window; Reported the ones escalated with no handoff.
	Running  int      `json:"running"`
	Pending  []string `json:"pending,omitempty"`
	Reported []string `json:"reported,omitempty"`
	// Done is true once nothing is left running and no request is open.
	Done bool `json:"done"`
}

// hibernateResult is the reply to hibernate and its status read. Labels counts
// the seats selected per backend label, "none" included, for the confirm.
type hibernateResult struct {
	Teams  []hibernateTeamStatus `json:"teams"`
	Labels map[string]int        `json:"labels,omitempty"`
	Seats  int                   `json:"seats"`
}

func (d *Daemon) hibernateStatusOf(t api.Team) hibernateTeamStatus {
	st := hibernateTeamStatus{Team: t.Key(), State: "none"}
	if m := t.Hibernate; m != nil {
		st.State, st.Actor, st.Roles = string(m.State), m.Actor, m.Roles
		st.Since = m.Since.UTC().Format(time.RFC3339)
	}
	for i := range t.Roles {
		role := &t.Roles[i]
		if !t.Hibernate.Covers(role.Name) {
			continue
		}
		for _, s := range d.store.ListSessionsByTeamRole(t.Workspace, t.Name, role.Name) {
			if !s.State.CountsAsAlive() {
				continue
			}
			st.Running++
		}
	}
	for session, req := range t.HibernateRequests {
		if req.Escalated {
			st.Reported = append(st.Reported, session)
		} else {
			st.Pending = append(st.Pending, session)
		}
	}
	sort.Strings(st.Reported)
	sort.Strings(st.Pending)
	st.Done = t.Hibernate != nil && t.Hibernate.State == api.HibernateHibernated && st.Running == 0 && len(t.HibernateRequests) == 0
	return st
}

// teamsFor resolves the request's teams: the named ones, or every team for All.
func (d *Daemon) teamsFor(p hibernateParams) ([]api.Team, error) {
	if p.All == (len(p.Teams) > 0) {
		return nil, fmt.Errorf("name one or more workspace/team keys, or pass --all")
	}
	if p.All {
		return d.store.ListTeams(), nil
	}
	var out []api.Team
	for _, key := range p.Teams {
		t, err := d.store.GetTeam(key)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// handleHibernate starts a hibernate, ends one seat (--end), counts a dry run,
// or reads status.
func (d *Daemon) handleHibernate(params json.RawMessage) daemonrpc.Response {
	var p hibernateParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return daemonrpc.Response{Error: fmt.Sprintf("hibernate: bad params: %v", err)}
		}
	}
	if p.Status {
		var res hibernateResult
		teams := d.store.ListTeams()
		if len(p.Teams) > 0 {
			teams = nil
			for _, key := range p.Teams {
				if t, err := d.store.GetTeam(key); err == nil {
					teams = append(teams, t)
				}
			}
		}
		for _, t := range teams {
			if t.Hibernate != nil {
				res.Teams = append(res.Teams, d.hibernateStatusOf(t))
			}
		}
		return respond(res)
	}
	if p.End != "" {
		sess, err := d.store.GetSession(p.End)
		if err != nil {
			return daemonrpc.Response{Error: err.Error()}
		}
		if err := d.teamCtrl.HibernateEnd(sess.Workspace+"/"+sess.Team, p.End); err != nil {
			return daemonrpc.Response{Error: err.Error()}
		}
		return respond(hibernateResult{Seats: 1})
	}
	if d.suspendRec.Load() != nil || d.suspendHeld.Load() {
		return daemonrpc.Response{Error: "hibernate refused: the cluster is suspended; run marvel resume first"}
	}
	teams, err := d.teamsFor(p)
	if err != nil {
		return daemonrpc.Response{Error: err.Error()}
	}
	for _, label := range append(slices.Clone(p.Backend), p.ExceptBackend...) {
		if label == "" {
			return daemonrpc.Response{Error: "hibernate: an empty backend label; name unlabeled roles with " + api.BackendNone}
		}
	}
	var res hibernateResult
	res.Labels = map[string]int{}
	selecting := len(p.Backend) > 0 || len(p.ExceptBackend) > 0
	type pick struct {
		key   string
		roles []string
	}
	var picks []pick
	for _, t := range teams {
		roles := api.SelectRoles(t.Roles, p.Backend, p.ExceptBackend)
		if len(roles) == 0 {
			continue
		}
		for _, name := range roles {
			for i := range t.Roles {
				if t.Roles[i].Name != name {
					continue
				}
				n := 0
				for _, s := range d.store.ListSessionsByTeamRole(t.Workspace, t.Name, name) {
					if s.State == api.SessionRunning && t.Roles[i].Runtime.Mode != api.RuntimeModeHeadless {
						n++
					}
				}
				res.Labels[api.BackendLabel(t.Roles[i])] += n
				res.Seats += n
			}
		}
		pk := pick{key: t.Key()}
		if selecting && len(roles) < len(t.Roles) {
			pk.roles = roles
		}
		picks = append(picks, pk)
	}
	if p.DryRun {
		return respond(res)
	}
	for _, pk := range picks {
		if _, err := d.teamCtrl.Hibernate(pk.key, teamHibernateOptions(pk.roles)); err != nil {
			return daemonrpc.Response{Error: err.Error()}
		}
	}
	d.teamCtrl.ReconcileOnce()
	for _, pk := range picks {
		if t, err := d.store.GetTeam(pk.key); err == nil {
			res.Teams = append(res.Teams, d.hibernateStatusOf(t))
		}
	}
	return respond(res)
}

// wakeParams is the wake request.
type wakeParams struct {
	Teams      []string `json:"teams,omitempty"`
	All        bool     `json:"all,omitempty"`
	SkipChecks []string `json:"skip_checks,omitempty"`
	Order      []string `json:"order,omitempty"`
}

// wakeResult reports the preflight and the teams now waking.
type wakeResult struct {
	Checks []preflightCheck      `json:"checks"`
	Teams  []hibernateTeamStatus `json:"teams,omitempty"`
}

// preflightCheck is one named check: passed or skipped or failed, and a note.
type preflightCheck struct {
	Name    string `json:"name"`
	Result  string `json:"result"`
	Detail  string `json:"detail,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
}

// handleWake runs the preflight, then wakes each team. A failed check stops the
// wake before any team changes, unless the operator skipped it by name.
func (d *Daemon) handleWake(params json.RawMessage) daemonrpc.Response {
	var p wakeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return daemonrpc.Response{Error: fmt.Sprintf("wake: bad params: %v", err)}
		}
	}
	if d.suspendRec.Load() != nil || d.suspendHeld.Load() {
		return daemonrpc.Response{Error: "wake refused: the cluster is suspended; run marvel resume first"}
	}
	var targets []api.Team
	all, err := d.teamsFor(hibernateParams{Teams: p.Teams, All: p.All})
	if err != nil {
		return daemonrpc.Response{Error: err.Error()}
	}
	for _, t := range all {
		if t.Hibernate != nil {
			targets = append(targets, t)
		} else if !p.All {
			return daemonrpc.Response{Error: fmt.Sprintf("wake refused: %s is not hibernated", t.Key())}
		}
	}
	if len(targets) == 0 {
		return daemonrpc.Response{Error: "wake: no hibernated team"}
	}
	for _, name := range p.SkipChecks {
		if !slices.Contains([]string{checkDaemon, checkBus, checkHarness}, name) {
			return daemonrpc.Response{Error: fmt.Sprintf("wake: unknown check %q (valid: %s, %s, %s)", name, checkDaemon, checkBus, checkHarness)}
		}
	}
	res := wakeResult{Checks: d.preflight(targets, p.SkipChecks)}
	for _, c := range res.Checks {
		if c.Result == "failed" {
			return wakeRefusal(res)
		}
	}
	if len(p.Order) > 0 {
		sorted := make([]api.Team, 0, len(targets))
		for _, key := range p.Order {
			for _, t := range targets {
				if t.Key() == key {
					sorted = append(sorted, t)
				}
			}
		}
		for _, t := range targets {
			if !slices.Contains(p.Order, t.Key()) {
				sorted = append(sorted, t)
			}
		}
		targets = sorted
	}
	for _, t := range targets {
		if err := d.teamCtrl.Wake(t.Key(), teamWakeOptions(p.SkipChecks)); err != nil {
			return daemonrpc.Response{Error: err.Error()}
		}
	}
	d.teamCtrl.ReconcileOnce()
	for _, t := range targets {
		if fresh, err := d.store.GetTeam(t.Key()); err == nil {
			res.Teams = append(res.Teams, d.hibernateStatusOf(fresh))
		}
	}
	return respond(res)
}

// wakeRefusal is the reply to a wake the preflight stopped, naming each failed
// check.
func wakeRefusal(res wakeResult) daemonrpc.Response {
	var failed []string
	for _, c := range res.Checks {
		if c.Result == "failed" {
			failed = append(failed, c.Name+": "+c.Detail)
		}
	}
	return daemonrpc.Response{Error: "wake refused by the preflight, nothing was started: " + strings.Join(failed, "; ") + ". Skip a check by name with --skip-check."}
}

// preflight runs the checks the woken teams need (design section 6, step 1):
// the daemon answers (it does, or this call would not be running), the bus links
// the cluster config declares are up, and every harness binary the woken roles
// name is present. Credentials are not read.
func (d *Daemon) preflight(targets []api.Team, skip []string) []preflightCheck {
	checks := []preflightCheck{
		{Name: checkDaemon, Result: "passed", Detail: "the daemon is answering"},
		d.busCheck(),
		d.harnessCheck(targets),
	}
	for i := range checks {
		if slices.Contains(skip, checks[i].Name) {
			checks[i].Skipped = true
			if checks[i].Result == "failed" {
				checks[i].Result = "skipped"
			}
		}
	}
	return checks
}

// busCheck passes when the cluster declares no bus link, and says so; it fails
// when a declared leaf link reads down.
func (d *Daemon) busCheck() preflightCheck {
	c := preflightCheck{Name: checkBus}
	st, ok := d.busStatus()
	switch {
	case !ok:
		c.Result, c.Detail = "passed", "the cluster config declares no bus, so there is no link to check"
	case st.Leaf == "down":
		c.Result, c.Detail = "failed", "the leaf link to the hub is down"
	case st.Leaf == "up":
		c.Result, c.Detail = "passed", "the leaf link to the hub is up"
	default:
		c.Result, c.Detail = "passed", fmt.Sprintf("the cluster config declares no hub link (leaf: %s)", st.Leaf)
	}
	return c
}

// harnessCheck fails when the command of a woken role is not on the path.
func (d *Daemon) harnessCheck(targets []api.Team) preflightCheck {
	c := preflightCheck{Name: checkHarness, Result: "passed"}
	missing := map[string]bool{}
	for _, t := range targets {
		for _, role := range t.Roles {
			if !t.Hibernate.Covers(role.Name) || role.Runtime.Command == "" {
				continue
			}
			if _, err := exec.LookPath(role.Runtime.Command); err != nil {
				missing[role.Runtime.Command] = true
			}
		}
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		c.Result, c.Detail = "failed", "not found: "+strings.Join(names, ", ")
	}
	return c
}

// hibernateInProgress reports whether any team still has a sign-off running: a
// mark in the hibernated state with seats left or requests open. Suspend refuses
// while one does (design section 3).
func (d *Daemon) hibernateInProgress() (string, bool) {
	for _, t := range d.store.ListTeams() {
		if t.Hibernate == nil {
			continue
		}
		if st := d.hibernateStatusOf(t); t.Hibernate.State == api.HibernateHibernated && !st.Done {
			return t.Key(), true
		}
	}
	return "", false
}
