package daemon

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// exposureVerbParams names one slot and one function, the shape of all three
// exposure verbs. Paths and Caster belong to add; Reason to void (marvel#945).
type exposureVerbParams struct {
	Workspace string   `json:"workspace"`
	Team      string   `json:"team"`
	Role      string   `json:"role"`
	Function  string   `json:"function"`
	Paths     []string `json:"paths,omitempty"`
	Caster    string   `json:"caster,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

func decodeExposureParams(params json.RawMessage) (exposureVerbParams, error) {
	var p exposureVerbParams
	if err := json.Unmarshal(params, &p); err != nil {
		return p, fmt.Errorf("bad params: %v", err)
	}
	if p.Workspace == "" || p.Team == "" || p.Role == "" {
		return p, fmt.Errorf("name the workspace, team and role of the slot")
	}
	return p, nil
}

func callerName(c caller) string {
	if c.fingerprint == "" {
		return "local"
	}
	return c.fingerprint
}

// emitExposure records an exposure action on the event ring and the log. A void
// is a warning: it releases a refusal, so it is loud.
func (d *Daemon) emitExposure(kind events.Kind, sev events.Severity, p exposureVerbParams, msg string) {
	events.Emit(d.events, events.Event{
		Kind:      kind,
		Severity:  sev,
		Workspace: p.Workspace,
		Team:      p.Team,
		Role:      p.Role,
		Message:   msg,
	})
}

// handleExposureAdd records that the slot read a function. The caster defaults
// to who called.
func (d *Daemon) handleExposureAdd(params json.RawMessage, c caller) Response {
	p, err := decodeExposureParams(params)
	if err != nil {
		return Response{Error: err.Error()}
	}
	if p.Caster == "" {
		p.Caster = callerName(c)
	}
	rec, err := d.store.RecordExposure(api.ExposureRecord{
		Workspace: p.Workspace, Team: p.Team, Role: p.Role, Function: p.Function,
		Paths: p.Paths, Caster: p.Caster,
	})
	if err != nil {
		return Response{Error: err.Error()}
	}
	d.emitExposure(events.KindExposureRecorded, events.SeverityInfo, p,
		fmt.Sprintf("slot %s/%s/%s read %s (recorded by %s)", p.Workspace, p.Team, p.Role, rec.Function, p.Caster))
	return respond(map[string]any{"function": rec.Function, "seq": rec.Seq})
}

// handleExposureEnd marks the slot's open readings of a function as ended. The
// refusal still counts them.
func (d *Daemon) handleExposureEnd(params json.RawMessage, c caller) Response {
	p, err := decodeExposureParams(params)
	if err != nil {
		return Response{Error: err.Error()}
	}
	n, err := d.store.EndExposure(p.Workspace, p.Team, p.Role, p.Function, time.Time{})
	if err != nil {
		return Response{Error: err.Error()}
	}
	d.emitExposure(events.KindExposureEnded, events.SeverityInfo, p,
		fmt.Sprintf("slot %s/%s/%s ended %d reading(s) of %s (by %s); they still count", p.Workspace, p.Team, p.Role, n, api.NormalizeFunction(p.Function), callerName(c)))
	return respond(map[string]any{"count": n})
}

// handleExposureVoid is the way out of a wrong record. It is served only on the
// local socket, which any seat on the host can reach, so it is not limited to
// the operator. It needs a reason, appends voided_at and the reason and deletes
// nothing, and says so on the event ring as a warning.
func (d *Daemon) handleExposureVoid(params json.RawMessage, c caller) Response {
	p, err := decodeExposureParams(params)
	if err != nil {
		return Response{Error: err.Error()}
	}
	n, err := d.store.VoidExposure(p.Workspace, p.Team, p.Role, p.Function, p.Reason, time.Time{})
	if err != nil {
		return Response{Error: err.Error()}
	}
	d.emitExposure(events.KindExposureVoided, events.SeverityWarning, p,
		fmt.Sprintf("slot %s/%s/%s: %d record(s) for %s VOIDED by %s: %s; the gate no longer counts them",
			p.Workspace, p.Team, p.Role, n, api.NormalizeFunction(p.Function), callerName(c), strings.TrimSpace(p.Reason)))
	return respond(map[string]any{"count": n})
}

// exposureConditions is the describe lines for a team: a role the gate is
// holding, and each voided record with its reason, so an operator reading the
// team sees why a role is not starting and what was released.
func (d *Daemon) exposureConditions(t api.Team) []roleCondition {
	var out []roleCondition
	for i := range t.Roles {
		role := &t.Roles[i]
		recs, err := d.store.ExposuresForSlot(t.Workspace, t.Name, role.Name)
		if err != nil {
			if len(role.Writes) > 0 {
				out = append(out, roleCondition{
					Role: role.Name, Type: "ExposureHeld", Status: "True",
					Message: "the exposure records cannot be read: " + err.Error(),
				})
			}
			continue
		}
		if held, reason := api.ExposureVerdict(d.store, t.Workspace, t.Name, role); held {
			out = append(out, roleCondition{Role: role.Name, Type: "ExposureHeld", Status: "True", Message: reason})
		}
		sort.Slice(recs, func(a, b int) bool { return recs[a].Seq < recs[b].Seq })
		for _, r := range recs {
			if r.VoidedAt.IsZero() {
				continue
			}
			out = append(out, roleCondition{
				Role: role.Name, Type: "ExposureVoided", Status: "True", Since: r.VoidedAt,
				Message: fmt.Sprintf("%s voided: %s", r.Function, r.VoidReason),
			})
		}
	}
	return out
}
