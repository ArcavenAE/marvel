package api

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Shift table parsing and validation: the list of automatic shift
// conditions (marvel#437, docs/design/shift-trigger-list.md D2).

// shiftKeysProbe reads each role's shift table as a raw map, so its keys can
// be checked against the ones this marvel understands. The manifest decoders
// drop unknown keys silently, and a strict decoder would apply to the whole
// manifest, so the shift table alone is decoded a second time here. The
// values are untyped because the point is to see keys the typed structs do
// not name.
type shiftKeysProbe struct {
	Teams []struct {
		Roles []struct {
			Shift map[string]any `toml:"shift" yaml:"shift"`
		} `toml:"role" yaml:"roles"`
	} `toml:"team" yaml:"teams"`
}

// mark records, on each role of m, the first unknown shift key and whether
// the table wrote an any key at all (so `any = []` is told from no list).
func (p shiftKeysProbe) mark(m *Manifest) {
	for i := range m.Teams {
		if i >= len(p.Teams) {
			return
		}
		for j := range m.Teams[i].Roles {
			if j >= len(p.Teams[i].Roles) {
				break
			}
			raw := p.Teams[i].Roles[j].Shift
			if raw == nil {
				continue
			}
			r := &m.Teams[i].Roles[j]
			_, r.shiftAnyPresent = raw["any"]
			r.shiftKeyErr = checkShiftKeys(raw)
		}
	}
}

var (
	shiftTableKeys = structKeys(reflect.TypeOf(ManifestShift{}))
	shiftEntryKeys = structKeys(reflect.TypeOf(ManifestShiftCondition{}))
)

// structKeys returns the toml key names a struct's fields declare. The toml
// and yaml tags of the shift structs carry the same names.
func structKeys(t reflect.Type) map[string]bool {
	keys := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("toml"), ",")
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}

// checkShiftKeys reports the first key, in sorted order, that the shift table
// or one of its any entries carries and this marvel does not understand.
func checkShiftKeys(raw map[string]any) error {
	if k := firstUnknown(raw, shiftTableKeys); k != "" {
		return fmt.Errorf("key %q is not supported by this marvel", k)
	}
	list, ok := raw["any"]
	if !ok {
		return nil
	}
	entries, err := shiftEntries(list)
	if err != nil {
		return err
	}
	for i, e := range entries {
		if k := firstUnknown(e, shiftEntryKeys); k != "" {
			return fmt.Errorf("any[%d]: key %q is not supported by this marvel", i, k)
		}
	}
	return nil
}

// shiftEntries normalizes the any list as the two decoders produce it: TOML
// gives []map[string]any, YAML gives []any of map[string]any.
func shiftEntries(list any) ([]map[string]any, error) {
	switch l := list.(type) {
	case []map[string]any:
		return l, nil
	case []any:
		out := make([]map[string]any, 0, len(l))
		for i, e := range l {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("any[%d] must be a table of one condition", i)
			}
			out = append(out, m)
		}
		return out, nil
	}
	return nil, fmt.Errorf("any must be a list of conditions")
}

func firstUnknown(raw map[string]any, known map[string]bool) string {
	var unknown []string
	for k := range raw {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	sort.Strings(unknown)
	return unknown[0]
}

// condition returns the single form's condition fields.
func (s *ManifestShift) condition() ManifestShiftCondition {
	return ManifestShiftCondition{
		On:             s.On,
		HeadroomTokens: s.HeadroomTokens,
		MaxAge:         s.MaxAge,
		QuietFor:       s.QuietFor,
		MaxDefer:       s.MaxDefer,
	}
}

// shiftPolicy validates a role's shift table and returns it as a ShiftPolicy, or
// an error naming what is wrong. where is "team[i].role[j]".
func (r *ManifestRole) shiftPolicy(where string) (*ShiftPolicy, error) {
	s := r.Shift
	if s == nil {
		return nil, nil
	}
	prefix := where + ".shift"
	if r.shiftKeyErr != nil {
		return nil, fmt.Errorf("%s: %w", prefix, r.shiftKeyErr)
	}
	single := s.condition() != ManifestShiftCondition{}
	hasAny := r.shiftAnyPresent || len(s.Any) > 0
	switch {
	case single && hasAny:
		return nil, fmt.Errorf("%s sets on and any together; use one form", prefix)
	case hasAny && len(s.Any) == 0:
		return nil, fmt.Errorf("%s.any is empty; omit the shift table for no automatic shift", prefix)
	case !single && !hasAny:
		return nil, fmt.Errorf("%s needs on or any", prefix)
	}

	entries := s.Any
	entryPrefix := func(i int) string { return fmt.Sprintf("%s.any[%d]", prefix, i) }
	if single {
		entries = []ManifestShiftCondition{s.condition()}
		entryPrefix = func(int) string { return prefix }
	}
	p := &ShiftPolicy{}
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		c, err := e.parse(entryPrefix(i))
		if err != nil {
			return nil, err
		}
		if seen[c.On] {
			return nil, fmt.Errorf("%s.any: on %q appears twice", prefix, c.On)
		}
		seen[c.On] = true
		if c.On == ShiftTriggerMaxAge && r.Runtime.Mode == RuntimeModeHeadless {
			return nil, fmt.Errorf("%s: max-age cannot apply to a headless role; a headless run past an age bound is a stuck run, and ending it is the kill branch, not a shift (design D7)", prefix)
		}
		if c.On == ShiftTriggerMaxAge && r.Replicas > 1 {
			return nil, MaxAgeReplicasError(prefix)
		}
		p.Any = append(p.Any, c)
	}
	if single {
		p.On, p.HeadroomTokens = s.On, s.HeadroomTokens
	}

	if (s.Handoff == "") != (s.HandoffMarker == "") {
		return nil, fmt.Errorf("%s: handoff and handoff_marker must be set together", prefix)
	}
	if s.Handoff != "" {
		if !filepath.IsAbs(s.Handoff) && !strings.HasPrefix(s.Handoff, "~/") {
			return nil, fmt.Errorf("%s.handoff %q must be absolute (or start with ~/)", prefix, s.Handoff)
		}
		for _, el := range strings.Split(filepath.ToSlash(s.Handoff), "/") {
			if el == ".." {
				return nil, fmt.Errorf("%s.handoff %q must not contain a .. element", prefix, s.Handoff)
			}
		}
		p.Handoff, p.HandoffMarker = s.Handoff, s.HandoffMarker
	}
	if s.HandoffWindow != "" {
		d, err := positiveDuration(prefix+".handoff_window", s.HandoffWindow)
		if err != nil {
			return nil, err
		}
		p.HandoffWindow = d
	}
	return p, nil
}

// parse validates one condition: a known trigger and exactly the fields it
// takes.
func (e ManifestShiftCondition) parse(prefix string) (ShiftCondition, error) {
	c := ShiftCondition{On: e.On}
	switch e.On {
	case ShiftTriggerContextPressure:
		if e.HeadroomTokens <= 0 {
			return c, fmt.Errorf("%s.headroom_tokens must be > 0 for on=%q", prefix, e.On)
		}
		for _, f := range [...]struct{ name, v string }{{"max_age", e.MaxAge}, {"quiet_for", e.QuietFor}, {"max_defer", e.MaxDefer}} {
			if f.v != "" {
				return c, fmt.Errorf("%s.%s does not apply to on=%q", prefix, f.name, e.On)
			}
		}
		c.HeadroomTokens = e.HeadroomTokens
	case ShiftTriggerMaxAge:
		if e.HeadroomTokens != 0 {
			return c, fmt.Errorf("%s.headroom_tokens does not apply to on=%q", prefix, e.On)
		}
		if e.MaxAge == "" {
			return c, fmt.Errorf("%s.max_age is required for on=%q", prefix, e.On)
		}
		d, err := positiveDuration(prefix+".max_age", e.MaxAge)
		if err != nil {
			return c, err
		}
		if d < MinShiftMaxAge {
			return c, fmt.Errorf("%s.max_age %s must be at least %s, so a typo cannot rotate a team every few minutes", prefix, e.MaxAge, MinShiftMaxAge)
		}
		c.MaxAge = d
		if e.QuietFor != "" {
			if c.QuietFor, err = positiveDuration(prefix+".quiet_for", e.QuietFor); err != nil {
				return c, err
			}
		}
		if e.MaxDefer != "" {
			if c.MaxDefer, err = positiveDuration(prefix+".max_defer", e.MaxDefer); err != nil {
				return c, err
			}
		}
	default:
		return c, fmt.Errorf("%s.on %q is not valid (valid: %q, %q)", prefix, e.On, ShiftTriggerContextPressure, ShiftTriggerMaxAge)
	}
	return c, nil
}

func positiveDuration(field, v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s %q: %w", field, v, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s %q must be positive", field, v)
	}
	return d, nil
}

// MaxAgeReplicasError is the refusal of a max-age condition on a role with
// more than one replica, shared by apply and scale so both doors say the same
// thing (marvel#452). where names the role's shift table.
func MaxAgeReplicasError(where string) error {
	return fmt.Errorf("%s: max-age cannot apply to a role with replicas > 1; its handoff request asks one seat but the shift drains every seat of the role, which would retire seats never asked for a handoff (marvel#452, design D5); use replicas = 1, or context-pressure", where)
}
