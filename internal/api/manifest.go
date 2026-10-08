package api

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// canonicalPermissionModes is the set of values Claude Code's
// --permission-mode flag accepts. ManifestRole.Permissions is passed
// through verbatim as --permission-mode by the forestage/claude adapters,
// so a value outside this set silently produces a broken session — the
// harness rejects the flag and the pane exits immediately. Validation
// rejects out-of-set values at parse time instead.
//
// An empty Permissions string is valid and means "unset — use the adapter
// default"; only non-empty out-of-set values are errors. dangerous_permissions
// is an orthogonal boolean (it appends --dangerously-skip-permissions) and is
// intentionally NOT part of this set: the two combine freely. See aae-orc-6spa.
var canonicalPermissionModes = map[string]bool{
	"acceptEdits":       true,
	"auto":              true,
	"bypassPermissions": true,
	"default":           true,
	"dontAsk":           true,
	"plan":              true,
}

// permissionModeList returns the canonical permission modes sorted and
// comma-joined, for inclusion in validation error messages.
func permissionModeList() string {
	modes := make([]string, 0, len(canonicalPermissionModes))
	for m := range canonicalPermissionModes {
		modes = append(modes, m)
	}
	sort.Strings(modes)
	return strings.Join(modes, ", ")
}

// Manifest represents a manifest declaring desired state.
// Supports both YAML (default) and TOML formats.
type Manifest struct {
	Workspace ManifestWorkspace  `toml:"workspace" yaml:"workspace"`
	Teams     []ManifestTeam     `toml:"team"      yaml:"teams"`
	Endpoints []ManifestEndpoint `toml:"endpoint"   yaml:"endpoints"`
	Policies  []ManifestPolicy   `toml:"policy"     yaml:"policies"`
}

// ManifestPolicy is a policy section of a manifest — a named Claude Code
// settings fragment marvel projects into a per-session file. Settings is
// carried verbatim; marvel does not interpret it.
type ManifestPolicy struct {
	Name     string         `toml:"name"              yaml:"name"`
	Version  string         `toml:"version,omitempty" yaml:"version,omitempty"`
	Settings map[string]any `toml:"settings,omitempty" yaml:"settings,omitempty"`
}

// ManifestWorkspace is the workspace section of a manifest.
type ManifestWorkspace struct {
	Name string `toml:"name" yaml:"name"`
	// Root is the filesystem root the workspace lives in; marvel work fills
	// it from the manifest's directory when absent.
	Root string `toml:"root,omitempty" yaml:"root,omitempty"`
	// TrustedFolders lists the operator's own repos a codex seat may trust.
	// A pointer, so an absent key (leave the stored list alone) differs from an
	// empty list (revoke it); omitempty on a plain slice would lose that.
	TrustedFolders *[]string `toml:"trusted_folders,omitempty" yaml:"trusted_folders,omitempty"`
}

// ManifestTeam is a team section of a manifest.
type ManifestTeam struct {
	Name string `toml:"name" yaml:"name"`
	// WorkDir is where the team's sessions run unless a role declares its
	// own. Relative values resolve against workspace.root only.
	WorkDir string          `toml:"workdir,omitempty" yaml:"workdir,omitempty"`
	Budget  *ManifestBudget `toml:"budget,omitempty" yaml:"budget,omitempty"`
	Roles   []ManifestRole  `toml:"role"  yaml:"roles"`
}

// ManifestBudget is the budget section within a team — the operator's
// declaration that a metered value may refuse a spawn for this team.
//
// A pointer on ManifestTeam so "absent" and "all zeros" stay
// distinguishable in both formats. The three unimplemented dimensions are
// declared here and nowhere else: yaml.v3 and BurntSushi/toml both drop
// undeclared fields silently (the defect the DroppedFields tests exist to
// catch), so declaring them is what lets validation reject a manifest
// naming one instead of accepting it as a no-op. They are never copied to
// api.Budget.
type ManifestBudget struct {
	MaxSessions  int            `toml:"max_sessions,omitempty"   yaml:"max_sessions,omitempty"`
	MaxTokens    int            `toml:"max_tokens,omitempty"     yaml:"max_tokens,omitempty"`
	OnUnmeasured UnmeasuredMode `toml:"on_unmeasured,omitempty"  yaml:"on_unmeasured,omitempty"`

	MaxCostUSD       float64 `toml:"max_cost_usd,omitempty"            yaml:"max_cost_usd,omitempty"`
	MaxTeamRSSBytes  int64   `toml:"max_team_rss_bytes,omitempty"      yaml:"max_team_rss_bytes,omitempty"`
	MaxSessionCtxPct float64 `toml:"max_session_ctx_percent,omitempty" yaml:"max_session_ctx_percent,omitempty"`
}

// Budget converts a declared block into the runtime ceiling. A nil
// receiver (no budget block) yields the zero Budget, which declares no
// gate. Only implemented dimensions cross over; the registered-but-
// unenforced ones are rejected at parse time and never reach here.
func (b *ManifestBudget) Budget() Budget {
	if b == nil {
		return Budget{}
	}
	return Budget{
		MaxSessions:  b.MaxSessions,
		MaxTokens:    b.MaxTokens,
		OnUnmeasured: b.OnUnmeasured,
	}
}

// ManifestRole is a role section within a team.
// Name is the job function. Persona and Identity are the costume and lens.
type ManifestRole struct {
	// SettingsSources declares which settings sources a bare claude seat
	// loads, from user, project and local. Omitted, every source loads, as
	// before this key existed.
	SettingsSources []string `toml:"settings_sources,omitempty" yaml:"settings_sources,omitempty"`
	Name            string   `toml:"name"                          yaml:"name"`
	Replicas        int      `toml:"replicas"                      yaml:"replicas"`
	// WorkDir is where this role's sessions run. Relative values resolve
	// against workspace.root only.
	WorkDir string `toml:"workdir,omitempty" yaml:"workdir,omitempty"`
	// shiftKeyErr and shiftAnyPresent come from shiftKeysProbe: the first
	// shift key this marvel does not understand, and whether the table wrote
	// an any key at all.
	shiftKeyErr     error
	shiftAnyPresent bool
	// replicasAbsent is set when the source document has no replicas key
	// for this role. An omitted count must not read as 0, which parks the
	// role (marvel#335). A role built in code is taken as declared.
	replicasAbsent       bool
	Runtime              ManifestRuntime      `toml:"runtime"                       yaml:"runtime"`
	RestartPolicy        string               `toml:"restart_policy,omitempty"      yaml:"restart_policy,omitempty"`
	MaxRestarts          int                  `toml:"max_restarts,omitempty"        yaml:"max_restarts,omitempty"`
	Permissions          string               `toml:"permissions,omitempty"         yaml:"permissions,omitempty"`
	DangerousPermissions bool                 `toml:"dangerous_permissions,omitempty" yaml:"dangerous_permissions,omitempty"`
	Persona              string               `toml:"persona,omitempty"             yaml:"persona,omitempty"`
	Identity             string               `toml:"identity,omitempty"            yaml:"identity,omitempty"`
	GlobalRole           string               `toml:"global_role,omitempty"         yaml:"global_role,omitempty"`
	Policy               string               `toml:"policy,omitempty"              yaml:"policy,omitempty"`
	HealthCheck          *ManifestHealthCheck `toml:"healthcheck,omitempty"         yaml:"healthcheck,omitempty"`
	// Shift opts this role into automatic shifts. Unset means the role shifts
	// only on an operator `marvel shift`. Parsed into Role.Shift.
	Shift *ManifestShift `toml:"shift,omitempty"               yaml:"shift,omitempty"`
	// ActivityTimeout is a duration string ("10m") opting this role into the
	// activity-staleness advisory (aae-orc-9box). Empty/unset disables it.
	// Parsed into Role.ActivityTimeout, the same string→duration shape as
	// healthcheck.timeout.
	ActivityTimeout string `toml:"activity_timeout,omitempty"    yaml:"activity_timeout,omitempty"`
	// Schedule puts a headless role on a clock (ADR-010 Amendment 1,
	// docs/design/scheduled-runs.md). Parsed into Role.Schedule.
	Schedule *ManifestSchedule `toml:"schedule,omitempty"            yaml:"schedule,omitempty"`
	// Views declares read-only views of a repository's default branch, one
	// per repository (docs/design/readonly-view.md section 2). Parsed into
	// Role.Views.
	Views []ManifestView `toml:"view,omitempty"                yaml:"views,omitempty"`
}

// ManifestView is one [[team.role.view]] entry. The durations are strings
// ("10m"), parsed and defaulted when the manifest is applied.
type ManifestView struct {
	Name         string `toml:"name"                    yaml:"name"`
	Remote       string `toml:"remote"                  yaml:"remote"`
	Ref          string `toml:"ref"                     yaml:"ref"`
	RefreshEvery string `toml:"refresh_every,omitempty" yaml:"refresh_every,omitempty"`
	ReenterGrace string `toml:"reenter_grace,omitempty" yaml:"reenter_grace,omitempty"`
}

// ManifestHealthCheck is the healthcheck section within a role.
type ManifestHealthCheck struct {
	Type             string `toml:"type"                         yaml:"type"`
	Timeout          string `toml:"timeout,omitempty"             yaml:"timeout,omitempty"`
	FailureThreshold int    `toml:"failure_threshold,omitempty"   yaml:"failure_threshold,omitempty"`
}

// ManifestShift is the automatic-shift section within a role. It takes one
// of two forms (docs/design/shift-trigger-list.md D2): the single form, the
// condition fields on the table itself, or the list form, Any. Either way it
// normalizes to ShiftPolicy.Any. Any key this struct does not name is an
// error at parse (checkShiftKeys), so `action`, `all` and nested `any` fail
// loudly on this marvel instead of vanishing.
type ManifestShift struct {
	On             string                   `toml:"on,omitempty"              yaml:"on,omitempty"`
	HeadroomTokens int                      `toml:"headroom_tokens,omitempty" yaml:"headroom_tokens,omitempty"`
	MaxAge         string                   `toml:"max_age,omitempty"         yaml:"max_age,omitempty"`
	QuietFor       string                   `toml:"quiet_for,omitempty"       yaml:"quiet_for,omitempty"`
	MaxDefer       string                   `toml:"max_defer,omitempty"       yaml:"max_defer,omitempty"`
	Any            []ManifestShiftCondition `toml:"any,omitempty"             yaml:"any,omitempty"`
	Handoff        string                   `toml:"handoff,omitempty"         yaml:"handoff,omitempty"`
	HandoffMarker  string                   `toml:"handoff_marker,omitempty"  yaml:"handoff_marker,omitempty"`
	HandoffWindow  string                   `toml:"handoff_window,omitempty"  yaml:"handoff_window,omitempty"`
}

// ManifestShiftCondition is one entry of a shift table's any list.
type ManifestShiftCondition struct {
	On             string `toml:"on,omitempty"              yaml:"on,omitempty"`
	HeadroomTokens int    `toml:"headroom_tokens,omitempty" yaml:"headroom_tokens,omitempty"`
	MaxAge         string `toml:"max_age,omitempty"         yaml:"max_age,omitempty"`
	QuietFor       string `toml:"quiet_for,omitempty"       yaml:"quiet_for,omitempty"`
	MaxDefer       string `toml:"max_defer,omitempty"       yaml:"max_defer,omitempty"`
}

// ManifestRuntime is the runtime section within a role.
type ManifestRuntime struct {
	Image   string      `toml:"image"          yaml:"image"`
	Command string      `toml:"command"        yaml:"command"`
	Args    []string    `toml:"args,omitempty"  yaml:"args,omitempty"`
	Script  string      `toml:"script,omitempty" yaml:"script,omitempty"`
	Mode    RuntimeMode `toml:"mode,omitempty"  yaml:"mode,omitempty"`
	Prompt  string      `toml:"prompt,omitempty" yaml:"prompt,omitempty"`
	// ContextWindow overrides the model-to-limit table, in tokens.
	ContextWindow int `toml:"context_window,omitempty" yaml:"context_window,omitempty"`
	// ContextFeed opts an interactive session into cooperative context
	// reporting. Only "statusline" is understood. See api.Runtime.
	ContextFeed string `toml:"context_feed,omitempty" yaml:"context_feed,omitempty"`
	// Env is the per-role environment override (Layer B, the backend
	// declaration surface). Merged over marvel's base environment at spawn.
	// See api.Runtime.Env.
	Env map[string]string `toml:"env,omitempty" yaml:"env,omitempty"`
	// Backend is the per-role INTENDED backend label, compared against the
	// resolved backend at spawn for loud failure. See api.Runtime.Backend.
	Backend string `toml:"backend,omitempty" yaml:"backend,omitempty"`
}

// ManifestEndpoint is an endpoint section of a manifest.
type ManifestEndpoint struct {
	Name string `toml:"name" yaml:"name"`
	Team string `toml:"team" yaml:"team"`
}

// ParseManifest reads and parses a manifest file. The format is detected
// from the file extension: .yaml/.yml for YAML, .toml for TOML.
// YAML is the default for ambiguous extensions.
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".toml":
		return parseManifestTOML(data)
	default:
		return parseManifestYAML(data)
	}
}

// ParseManifestBytes parses manifest content whose format is not known
// from a filename: YAML first (the default), TOML otherwise. This is the
// path every `marvel work` takes, because the CLI sends bytes.
//
// Format is settled BEFORE validation runs, and on the required field
// rather than on unmarshal success. Validating inside each attempt made
// every YAML validation failure fall through to the TOML parser, so an
// operator who declared 40 replicas under a 6-session ceiling was told
// "toml: line 72: expected '.' or '='" instead of which clause they broke.
// That masked the declaration clause, the unenforced-dimension rejection,
// and the on_unmeasured typo check alike, on the only apply path there is.
//
// Deciding on Workspace.Name is what keeps TOML working: yaml.Unmarshal
// tolerates some TOML input and yields a manifest with nothing in it, and
// a manifest with no workspace name is not a YAML manifest marvel could
// have applied anyway.
func ParseManifestBytes(data []byte) (*Manifest, error) {
	ym, yerr := unmarshalManifestYAML(data)
	if yerr == nil && ym.Workspace.Name != "" {
		return validateManifest(ym)
	}
	tm, terr := unmarshalManifestTOML(data)
	if terr == nil {
		return validateManifest(tm)
	}
	if yerr != nil {
		// YAML is the documented default, so its error leads; a TOML syntax
		// complaint about a YAML file names the wrong language. The TOML
		// error rides along for a genuine TOML file that failed to parse.
		return nil, fmt.Errorf("%w (also tried TOML: %v)", yerr, terr)
	}
	// Parsed as YAML but named no workspace, and TOML refused it: report the
	// missing required field rather than a syntax error about the other
	// format.
	return validateManifest(ym)
}

func parseManifestYAML(data []byte) (*Manifest, error) {
	m, err := unmarshalManifestYAML(data)
	if err != nil {
		return nil, err
	}
	return validateManifest(m)
}

func parseManifestTOML(data []byte) (*Manifest, error) {
	m, err := unmarshalManifestTOML(data)
	if err != nil {
		return nil, err
	}
	return validateManifest(m)
}

func unmarshalManifestYAML(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse yaml manifest: %w", err)
	}
	var probe replicasProbe
	if err := yaml.Unmarshal(data, &probe); err == nil {
		probe.mark(&m)
	}
	var shiftProbe shiftKeysProbe
	if err := yaml.Unmarshal(data, &shiftProbe); err == nil {
		shiftProbe.mark(&m)
	}
	return &m, nil
}

func unmarshalManifestTOML(data []byte) (*Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse toml manifest: %w", err)
	}
	var probe replicasProbe
	if err := toml.Unmarshal(data, &probe); err == nil {
		probe.mark(&m)
	}
	var shiftProbe shiftKeysProbe
	if err := toml.Unmarshal(data, &shiftProbe); err == nil {
		shiftProbe.mark(&m)
	}
	return &m, nil
}

// replicasProbe reads only whether each role wrote a replicas key. Since 0
// is legal desired state (a parked role, marvel#335), the int field alone
// cannot tell "replicas: 0" from a role that left the key out.
type replicasProbe struct {
	Teams []struct {
		Roles []struct {
			Replicas *int `toml:"replicas" yaml:"replicas"`
		} `toml:"role" yaml:"roles"`
	} `toml:"team" yaml:"teams"`
}

// mark flags the roles of m whose replicas key was absent in the source.
func (p replicasProbe) mark(m *Manifest) {
	for i := range m.Teams {
		if i >= len(p.Teams) {
			return
		}
		for j := range m.Teams[i].Roles {
			if j >= len(p.Teams[i].Roles) {
				break
			}
			m.Teams[i].Roles[j].replicasAbsent = p.Teams[i].Roles[j].Replicas == nil
		}
	}
}

func validateManifest(m *Manifest) (*Manifest, error) {
	if m.Workspace.Name == "" {
		return nil, fmt.Errorf("parse manifest: workspace.name is required")
	}
	policyNames := make(map[string]bool, len(m.Policies))
	for i, p := range m.Policies {
		if p.Name == "" {
			return nil, fmt.Errorf("parse manifest: policy[%d].name is required", i)
		}
		if policyNames[p.Name] {
			return nil, fmt.Errorf("parse manifest: policy[%d].name %q is duplicated", i, p.Name)
		}
		policyNames[p.Name] = true
	}
	teamNames := make(map[string]bool, len(m.Teams))
	for i, t := range m.Teams {
		if t.Name == "" {
			return nil, fmt.Errorf("parse manifest: team[%d].name is required", i)
		}
		// A second entry would overwrite the first at apply time, so the
		// operator would get one team where they wrote two (marvel#319).
		if teamNames[t.Name] {
			return nil, fmt.Errorf("parse manifest: team[%d].name %q is duplicated", i, t.Name)
		}
		teamNames[t.Name] = true
		if len(t.Roles) == 0 {
			return nil, fmt.Errorf("parse manifest: team[%d] must have at least one role", i)
		}
		for j, r := range t.Roles {
			if r.Name == "" {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].name is required", i, j)
			}
			// 0 is legal and parks the role (marvel#335); the bound is the
			// one marvel scale applies, from the same validator.
			if r.replicasAbsent {
				return nil, fmt.Errorf("parse manifest: team %s role %s: replicas is required (0 parks the role)", t.Name, r.Name)
			}
			if err := ValidateReplicas(t.Name, r.Name, r.Replicas); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			if r.Runtime.Image == "" && r.Runtime.Command == "" {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].runtime needs image or command", i, j)
			}
			// A negative window would silently produce a negative
			// denominator and a nonsense percentage; 0 means unset.
			if r.Runtime.ContextWindow < 0 {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].runtime.context_window must be >= 0", i, j)
			}
			// Like permissions: empty means unset, but a non-empty typo
			// would silently project nothing, so reject it here.
			if r.Runtime.ContextFeed != "" && r.Runtime.ContextFeed != ContextFeedStatusline {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].runtime.context_feed %q is not valid (valid: %q)", i, j, r.Runtime.ContextFeed, ContextFeedStatusline)
			}
			if r.Policy != "" && !policyNames[r.Policy] {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d] references undefined policy %q", i, j, r.Policy)
			}
			// An unrecognized trigger or key would silently never fire, and a
			// bad threshold would fire on the first sample or never. Reject
			// them so a misconfigured trigger is an error at apply, not a
			// no-op at runtime (marvel#437 D2).
			if _, err := r.views(fmt.Sprintf("parse manifest: team[%d].role[%d]", i, j)); err != nil {
				return nil, err
			}
			policy, err := r.shiftPolicy(fmt.Sprintf("team[%d].role[%d]", i, j))
			if err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			// A headroom that is not below the window the role declares
			// would hold at any occupancy: the remainder test is
			// tokens > window - headroom, and that bound is zero or less.
			if err := checkHeadroomBelowWindow(fmt.Sprintf("team[%d].role[%d]", i, j), policy, r.Runtime.ContextWindow); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			if err := validateSettingsSources(fmt.Sprintf("team[%d].role[%d]", i, j), r.SettingsSources); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			if r.Schedule != nil {
				if err := validateSchedule(t.Name, r, time.Now().UTC()); err != nil {
					return nil, fmt.Errorf("parse manifest: %w", err)
				}
			}
			// Permissions maps verbatim to --permission-mode; an empty
			// value means "unset" and is allowed, but a non-empty typo
			// silently breaks the session, so reject it here. Orthogonal
			// to dangerous_permissions (see canonicalPermissionModes).
			// global_role names the global role the role holds, as the address
			// word. Empty is absent and "none" opts out; anything else is a
			// typo, and a typo would silently hold nothing, so refuse it.
			if r.GlobalRole != "" && r.GlobalRole != GlobalRoleSupervisor && r.GlobalRole != GlobalRoleNone {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].global_role %q is not a valid global role (valid: %s, %s)", i, j, r.GlobalRole, GlobalRoleNone, GlobalRoleSupervisor)
			}
			if r.Permissions != "" && !canonicalPermissionModes[r.Permissions] {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].permissions %q is not a valid permission mode (valid: %s)", i, j, r.Permissions, permissionModeList())
			}
		}
		// After the role loop, so the replica sum is available to the
		// declaration clause.
		if err := validateManifestBudget(i, t); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// validateManifestBudget applies the dimension registry's rules plus the
// declaration clause to one team's budget block.
//
// The declaration clause (sum of replicas must fit under max_sessions) is
// the load-bearing one. It makes declared <= limit an invariant of every
// parsed manifest, which is what makes converging a role toward its
// declared replicas provably safe: repair can never cross the team cap, so
// no "is this growth?" predicate exists anywhere in the reconciler and a
// crashed replica can never be refused its replacement. It also catches
// the motivating failure (a declared 40-crew fan-out under a 6-session
// ceiling) before any daemon state is touched.
func validateManifestBudget(i int, t ManifestTeam) error {
	b := t.Budget
	if b == nil {
		return nil
	}
	// A negative ceiling silently inverts the comparison; 0 means unset.
	// Same rationale as runtime.context_window above.
	if b.MaxSessions < 0 {
		return fmt.Errorf("parse manifest: team[%d].budget.max_sessions must be >= 0", i)
	}
	if b.MaxTokens < 0 {
		return fmt.Errorf("parse manifest: team[%d].budget.max_tokens must be >= 0", i)
	}
	// Registered-but-unenforced dimensions are rejected rather than
	// dropped, driven by the registry so a future row needs no new branch.
	unenforced := []struct {
		dim Dimension
		set bool
	}{
		{DimMaxCostUSD, b.MaxCostUSD != 0},
		{DimMaxTeamRSSBytes, b.MaxTeamRSSBytes != 0},
		{DimMaxSessionCtxPct, b.MaxSessionCtxPct != 0},
	}
	for _, u := range unenforced {
		if !u.set {
			continue
		}
		spec, ok := LookupDimension(u.dim)
		if !ok {
			return fmt.Errorf("parse manifest: team[%d].budget.%s is not a known dimension (valid: %s)", i, u.dim, DimensionList())
		}
		return fmt.Errorf("parse manifest: team[%d].budget.%s is a known dimension (matrix row %d) but is not enforced in this slice; owner %s", i, u.dim, spec.MatrixRow, spec.Owner)
	}
	if b.OnUnmeasured != "" && !canonicalUnmeasuredModes[b.OnUnmeasured] {
		return fmt.Errorf("parse manifest: team[%d].budget.on_unmeasured %q is not valid (valid: %s)", i, b.OnUnmeasured, unmeasuredModeList())
	}
	if b.MaxSessions > 0 {
		declared := 0
		for _, r := range t.Roles {
			declared += r.Replicas
		}
		if declared > b.MaxSessions {
			return fmt.Errorf("parse manifest: team[%d] declares %d replicas across %d role(s) but budget.max_sessions is %d", i, declared, len(t.Roles), b.MaxSessions)
		}
	}
	return nil
}

// StreamCapableRole reports whether a role's harness can publish the usage
// stream a token ceiling is measured from.
//
// Injected rather than computed here because the answer lives in the
// adapter registry, and internal/runtime imports this package: mode alone
// cannot answer it. A generic role declaring mode: headless satisfies every
// mode check and still can never emit a token, because the generic adapter
// implements no stream path at all.
type StreamCapableRole func(ManifestRole) bool

// ValidateBudgets is the host-side pre-flight sibling of ValidateRuntimes:
// it reports every team where a declared dimension is enforceable against
// NO role in the team, so a mute gate becomes an apply-time error instead
// of a silent no-op. Same class of fix as ArcavenAE/marvel#9.
//
// Only max_tokens is capability-dependent. Token usage arrives on a harness
// stream, so a team with no stream-capable headless role can never report a
// token to count. The threshold is NO role rather than ANY role: a mixed
// team is allowed, because a partial total and on_unmeasured carry the
// honesty at runtime. max_sessions is counted from the store and depends on
// no harness.
//
// canStream is required. A nil predicate is a wiring error and is reported
// as one, because the alternative (falling back to the mode-only check) is
// the silent hole this function exists to close.
func (m *Manifest) ValidateBudgets(canStream StreamCapableRole) error {
	declaresTokens := false
	for _, t := range m.Teams {
		if t.Budget != nil && t.Budget.MaxTokens > 0 {
			declaresTokens = true
			break
		}
	}
	if !declaresTokens {
		return nil
	}
	if canStream == nil {
		return errors.New("budget pre-flight: no stream-capability predicate supplied, so budget.max_tokens cannot be checked for a role that could report it")
	}
	var mute []string
	for ti, t := range m.Teams {
		if t.Budget == nil || t.Budget.MaxTokens <= 0 {
			continue
		}
		reporter := false
		for _, r := range t.Roles {
			if canStream(r) {
				reporter = true
				break
			}
		}
		if !reporter {
			mute = append(mute, fmt.Sprintf("  team[%d=%s]: budget.max_tokens is declared but no role runs a stream-capable harness in headless mode, so no role can report token usage; marvel would never enforce this ceiling", ti, t.Name))
		}
	}
	if len(mute) > 0 {
		return fmt.Errorf("budget pre-flight failed on %d team(s):\n%s", len(mute), strings.Join(mute, "\n"))
	}
	return nil
}

// ContextFeedCapableRole reports whether a role's harness can honour
// runtime.context_feed. Injected for the reason StreamCapableRole is: the
// answer lives in the adapter registry.
type ContextFeedCapableRole func(ManifestRole) bool

// ContextFeedAdvisories lists every role that declares runtime.context_feed
// on a harness that cannot honour it. It is advisory, not a refusal: the
// role still runs, it just gets no feed from that line, which is how a
// policy on a runtime with no settings surface is treated. What it removes
// is the silence: before this, `marvel work` reported ready and CTX% stayed
// `-` with nothing to say why (orc finding-179 §3, the finding-044 shape).
func (m *Manifest) ContextFeedAdvisories(canFeed ContextFeedCapableRole) []string {
	if canFeed == nil {
		return nil
	}
	var out []string
	for _, t := range m.Teams {
		for _, r := range t.Roles {
			if r.Runtime.ContextFeed == "" || canFeed(r) {
				continue
			}
			runtimeName := r.Runtime.Image
			if runtimeName == "" {
				runtimeName = r.Runtime.Command
			}
			out = append(out, fmt.Sprintf("team %s role %s: runtime %q cannot honour context_feed %q; it is advisory and feeds nothing",
				t.Name, r.Name, runtimeName, r.Runtime.ContextFeed))
		}
	}
	return out
}

// ShiftHeadroomAdvisories lists every role that has a context-pressure arm but
// declares no runtime.context_window, so its headroom_tokens cannot be checked
// against the window at apply. It is advisory, not a refusal: the window may
// still resolve at runtime from the model table, and a role without one is
// skipped by the arm.
func (m *Manifest) ShiftHeadroomAdvisories() []string {
	var out []string
	for _, t := range m.Teams {
		for _, r := range t.Roles {
			if r.Runtime.ContextWindow > 0 {
				continue
			}
			policy, err := r.shiftPolicy(t.Name + "/" + r.Name)
			if err != nil || policy == nil {
				continue
			}
			for _, c := range policy.Conditions() {
				if c.On != ShiftTriggerContextPressure {
					continue
				}
				out = append(out, fmt.Sprintf("team %s role %s: shift on context-pressure with headroom_tokens %d, but runtime.context_window is not declared, so the headroom is not checked against the window",
					t.Name, r.Name, c.HeadroomTokens))
			}
		}
	}
	return out
}

// ValidateRuntimes checks that each role's runtime command (and script,
// if set) actually resolves on the daemon's host before the manifest
// is applied. Returns an aggregated error listing every missing binary
// so the operator sees all problems at once — not just the first.
//
// Only a command in the simple form is resolved (see validateCommand), by
// exec.Command semantics on its first whitespace field:
//   - Absolute path ("/usr/local/bin/forestage"): os.Stat must succeed.
//   - Path with separator ("bin/simulator", "./scripts/x"): resolved
//     against the directory the launch starts the role's pane in, not
//     the daemon CWD, via os.Stat.
//   - Plain name ("sleep", "forestage"): exec.LookPath searches $PATH.
//   - Empty command: flagged; the manifest parser already catches this
//     but we defend here so misuse of the public API surfaces clearly.
//   - Inline arguments ("claude --model x"): only the first whitespace
//     field is resolved; the rest are the program's arguments.
//
// Shell text it does not parse (an assignment, a quote, a substitution, a
// newline, a relative program with no directory) is not refused: the
// advisories returned say that role's program was not checked.
//
// Scripts are checked as absolute/relative paths (never PATH-resolved)
// because scripts are typically repo-relative files, not executables.
//
// See ArcavenAE/marvel#9 / aae-orc-rjm — the pre-fix behavior was to
// silently create panes whose processes exited immediately, hiding the
// real error behind a downstream "can't find pane" warning.
func (m *Manifest) ValidateRuntimes() ([]string, error) {
	var missing, advisories []string
	for ti, t := range m.Teams {
		for ri, r := range t.Roles {
			ctx := fmt.Sprintf("team[%d=%s].role[%d=%s]", ti, t.Name, ri, r.Name)
			dir := ResolveWorkDir("", teamAnchor(m.Workspace.Root, t.WorkDir), joinWorkDir(m.Workspace.Root, r.WorkDir))
			advisory, err := validateCommand(r.Runtime.Command, dir)
			if err != nil {
				missing = append(missing, fmt.Sprintf("  %s: command %q: %v", ctx, r.Runtime.Command, err))
			}
			if advisory != "" {
				advisories = append(advisories, fmt.Sprintf("role %s: %s", r.Name, advisory))
			}
			if r.Runtime.Script != "" {
				if err := validateScript(r.Runtime.Script); err != nil {
					missing = append(missing, fmt.Sprintf("  %s: script %q: %v", ctx, r.Runtime.Script, err))
				}
			}
		}
	}
	if len(missing) > 0 {
		return advisories, fmt.Errorf("runtime pre-flight failed on %d role(s):\n%s", len(missing), strings.Join(missing, "\n"))
	}
	return advisories, nil
}

// shellMeta are the characters that make the first field of a command more
// than a program name to the shell the launch hands it to: an assignment, a
// quote, an escape, an expansion, a list or pipe, a redirect, a group, a glob
// a tilde, or a hash that makes the rest a comment. A field with none of them
// is the program as written.
const shellMeta = "='\"\\$`;|&<>(){}*?[~#"

// validateCommand checks the program of a command. The launch passes the text
// to a shell (the driver hands tmux `env -u ... <command>` as one shell
// command), so the pre-flight may be less strict than the launch and never more
// (marvel#517).
//
// A command in the simple form, whose first field (words split on space and tab,
// as the shell does) has no shell metacharacter and which holds no other
// whitespace, newline included, has that field resolved: an absolute
// path by stat, a plain name on $PATH, a relative path against dir, and a miss
// refuses. dir is where the launch starts the pane (tmux -c), resolved as the
// controller places a session: the role's workdir, else the team's anchor, else
// the root (ResolveWorkDir over the values Apply stores). Anything else is not
// parsed and not refused; the advisory says its program was not checked. That
// includes a relative program for a role that has no directory, since the pane
// would start somewhere this process cannot name. It is not a shell parser and
// does not grow into one.
func validateCommand(command, dir string) (advisory string, err error) {
	if strings.ContainsRune(command, '\n') {
		// The shell ends the command at the newline and runs the next line as
		// another command, which fails with rc 127.
		return "runtime command spans several lines; the shell runs each line as its own command, so the pre-flight did not check its program", nil
	}
	// The shell splits words on space and tab only. Any other whitespace
	// (a no-break space, a carriage return, a vertical tab, a form feed) is
	// part of a word there, so splitting on it here would read a program the
	// launch does not.
	for _, r := range command {
		if r != ' ' && r != '\t' && unicode.IsSpace(r) {
			return commandNotParsedAdvisory, nil
		}
	}
	fields := strings.FieldsFunc(command, func(r rune) bool { return r == ' ' || r == '\t' })
	if len(fields) == 0 {
		return "", errors.New("empty")
	}
	cmd := fields[0]
	if strings.ContainsAny(cmd, shellMeta) {
		return commandNotParsedAdvisory, nil
	}
	// Path, absolute or with a separator: must exist on disk.
	if filepath.IsAbs(cmd) || strings.ContainsRune(cmd, filepath.Separator) {
		path := cmd
		if !filepath.IsAbs(cmd) {
			if dir == "" {
				return commandNotParsedAdvisory, nil
			}
			path = filepath.Join(dir, cmd)
		}
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("not found: %w", err)
		}
		return "", nil
	}
	// Plain name: must resolve on $PATH.
	if _, err := exec.LookPath(cmd); err != nil {
		return "", fmt.Errorf("not on PATH: %w", err)
	}
	return "", nil
}

// CommandWordsReadable is a red stub; the green commit gives it a body.
func CommandWordsReadable(string) bool { return true }

const commandNotParsedAdvisory = "runtime command is shell text the pre-flight does not parse; its program was not checked"

func validateScript(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("not found: %w", err)
	}
	return nil
}

// ValidateTeamNames refuses a team whose name is already applied in another
// workspace. Broker users are named by team (internal/bus), so two
// workspaces holding one team name break bus-auth rendering for the whole
// cluster, and that failure surfaces only on the event ring after the apply
// has succeeded and sessions are running. Refusing here, before anything is
// committed, is the current-state fix; the cross-workspace-team model, where
// one team may span workspaces, is the direction that supersedes it
// (marvel#319). Re-applying a team in a workspace that already holds it is
// unaffected, including either side of a pair stored before this check.
func (m *Manifest) ValidateTeamNames(existing []Team) error {
	holders := make(map[string][]string, len(existing))
	own := make(map[string]bool, len(existing))
	for _, t := range existing {
		if t.Workspace == m.Workspace.Name {
			own[t.Name] = true
			continue
		}
		holders[t.Name] = append(holders[t.Name], t.Workspace)
	}
	for _, mt := range m.Teams {
		// A workspace that already holds the team re-applies it, even when a
		// pair from before this check exists; refusing would lock both sides
		// of that pair out of their own teams.
		if own[mt.Name] {
			continue
		}
		ws := holders[mt.Name]
		if len(ws) == 0 {
			continue
		}
		sort.Strings(ws)
		quoted := make([]string, len(ws))
		for i, w := range ws {
			quoted[i] = strconv.Quote(w)
		}
		return fmt.Errorf("team %q is already applied in workspace %s; team names are cluster-wide because broker users are named by team, so rename it or delete %s/%s first",
			mt.Name, strings.Join(quoted, ", "), ws[0], mt.Name)
	}
	return nil
}

// Apply converts a manifest into store resources and creates them.
func (m *Manifest) Apply(store *Store) error {
	now := time.Now().UTC()

	ws := &Workspace{Name: m.Workspace.Name, Root: m.Workspace.Root, CreatedAt: now}
	if m.Workspace.TrustedFolders != nil {
		ws.TrustedFolders = slices.Clone(*m.Workspace.TrustedFolders)
	}
	// Ignore already-exists for workspace (idempotent apply), but a re-applied
	// root moves. An apply that names no root leaves a stored one alone.
	if err := store.CreateWorkspace(ws); err != nil {
		if !isAlreadyExists(err) {
			return fmt.Errorf("apply workspace: %w", err)
		}
		if m.Workspace.Root != "" || m.Workspace.TrustedFolders != nil {
			if err := store.UpdateWorkspace(ws.Name, func(live *Workspace) error {
				if m.Workspace.Root != "" {
					live.Root = m.Workspace.Root
				}
				// A list that is present replaces the stored one, an empty
				// list revokes it, and an absent key leaves it alone.
				if m.Workspace.TrustedFolders != nil {
					live.TrustedFolders = slices.Clone(*m.Workspace.TrustedFolders)
				}
				return nil
			}); err != nil {
				return fmt.Errorf("apply workspace: %w", err)
			}
		}
	}

	// Policies first, so a role's policy reference resolves against
	// already-present state and an edited policy is in the store before
	// the reconciler re-projects.
	for _, mp := range m.Policies {
		policy := &Policy{
			Name:      mp.Name,
			Workspace: m.Workspace.Name,
			Version:   mp.Version,
			Settings:  mp.Settings,
			CreatedAt: now,
		}
		if _, err := store.GetPolicy(policy.Key()); err == nil {
			if err := store.UpdatePolicy(policy.Key(), func(live *Policy) error {
				live.Version = mp.Version
				live.Settings = mp.Settings
				return nil
			}); err != nil {
				return fmt.Errorf("apply policy %s: %w", mp.Name, err)
			}
		} else if err := store.CreatePolicy(policy); err != nil {
			return fmt.Errorf("apply policy %s: %w", mp.Name, err)
		}
	}

	for _, mt := range m.Teams {
		var roles []Role
		for _, mr := range mt.Roles {
			rt := Runtime{
				Name:          mr.Runtime.Image,
				Command:       mr.Runtime.Command,
				Args:          mr.Runtime.Args,
				Script:        mr.Runtime.Script,
				Mode:          mr.Runtime.Mode,
				Prompt:        mr.Runtime.Prompt,
				ContextWindow: mr.Runtime.ContextWindow,
				ContextFeed:   mr.Runtime.ContextFeed,
				Env:           mr.Runtime.Env,
				Backend:       mr.Runtime.Backend,
			}
			if rt.Name == "" {
				rt.Name = rt.Command
			}
			role := Role{
				Name:                 mr.Name,
				Replicas:             mr.Replicas,
				Runtime:              rt,
				RestartPolicy:        RestartAlways,
				MaxRestarts:          mr.MaxRestarts,
				Permissions:          mr.Permissions,
				DangerousPermissions: mr.DangerousPermissions,
				Persona:              mr.Persona,
				GlobalRole:           mr.GlobalRole,
				Identity:             mr.Identity,
				Policy:               mr.Policy,
				WorkDir:              joinWorkDir(m.Workspace.Root, mr.WorkDir),
				SettingsSources:      mr.SettingsSources,
			}
			if mr.RestartPolicy != "" {
				role.RestartPolicy = RestartPolicy(mr.RestartPolicy)
			}
			if mr.ActivityTimeout != "" {
				d, err := time.ParseDuration(mr.ActivityTimeout)
				if err != nil {
					return fmt.Errorf("parse role %q activity_timeout %q: %w", mr.Name, mr.ActivityTimeout, err)
				}
				role.ActivityTimeout = d
			}
			if mr.HealthCheck != nil {
				timeout := 30 * time.Second
				if mr.HealthCheck.Timeout != "" {
					d, err := time.ParseDuration(mr.HealthCheck.Timeout)
					if err != nil {
						return fmt.Errorf("parse healthcheck timeout %q: %w", mr.HealthCheck.Timeout, err)
					}
					timeout = d
				}
				threshold := 3
				if mr.HealthCheck.FailureThreshold > 0 {
					threshold = mr.HealthCheck.FailureThreshold
				}
				role.HealthCheck = &HealthCheck{
					Type:             HealthCheckType(mr.HealthCheck.Type),
					Timeout:          timeout,
					FailureThreshold: threshold,
				}
			}
			shift, err := mr.shiftPolicy("team " + mt.Name + " role " + mr.Name)
			if err != nil {
				return fmt.Errorf("apply manifest: %w", err)
			}
			role.Shift = shift
			views, err := mr.views("team " + mt.Name + " role " + mr.Name)
			if err != nil {
				return fmt.Errorf("apply manifest: %w", err)
			}
			role.Views = views
			if mr.Schedule != nil {
				sched, err := mr.Schedule.policy()
				if err != nil {
					return fmt.Errorf("parse role %q schedule: %w", mr.Name, err)
				}
				role.Schedule = sched
			}
			roles = append(roles, role)
		}

		budget := mt.Budget.Budget()
		team := &Team{
			Name:       mt.Name,
			Workspace:  m.Workspace.Name,
			Roles:      roles,
			Budget:     budget,
			WorkDir:    teamAnchor(m.Workspace.Root, mt.WorkDir),
			Generation: 1,
			CreatedAt:  now,
		}
		// Update roles if team already exists; route through the store
		// lock so the mutation doesn't race concurrent readers. The budget
		// moves with the roles: without that line an edited budget applies
		// on create and is silently ignored on every re-apply, while an
		// edited role list takes effect — the worst available split.
		if _, err := store.GetTeam(team.Key()); err == nil {
			if err := store.UpdateTeam(team.Key(), func(live *Team) error {
				live.Roles = roles
				live.Budget = budget
				// An apply that declares no placement leaves the anchor the
				// team already has, a legacy stamp (the v1 to v2 migration's
				// record of where a root-less team already ran) included. Any
				// declaration replaces the anchor and so ends the stamp.
				if m.Workspace.Root != "" || mt.WorkDir != "" {
					live.WorkDir = team.WorkDir
					live.WorkDirSource = ""
				}
				return nil
			}); err != nil {
				return fmt.Errorf("apply team %s: %w", mt.Name, err)
			}
		} else {
			if err := store.CreateTeam(team); err != nil {
				return fmt.Errorf("apply team %s: %w", mt.Name, err)
			}
		}
	}

	for _, me := range m.Endpoints {
		ep := &Endpoint{
			Name:      me.Name,
			Workspace: m.Workspace.Name,
			Team:      me.Team,
		}
		if err := store.CreateEndpoint(ep); err != nil && !isAlreadyExists(err) {
			return fmt.Errorf("apply endpoint %s: %w", me.Name, err)
		}
	}

	return nil
}

func isAlreadyExists(err error) bool {
	return err != nil && err.Error() != "" && contains(err.Error(), "already exists")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// settingsSourceNames are the Claude Code settings sources a role may declare.
// marvel passes them as given and does not interpret the files they name.
var settingsSourceNames = map[string]bool{"user": true, "project": true, "local": true}

// validateSettingsSources refuses a declaration the harness would reject or
// that says nothing: an empty list, an unknown source, a repeat.
func validateSettingsSources(where string, sources []string) error {
	if sources == nil {
		return nil
	}
	if len(sources) == 0 {
		return fmt.Errorf("%s.settings_sources is empty; omit it for the default", where)
	}
	seen := make(map[string]bool, len(sources))
	for _, v := range sources {
		if !settingsSourceNames[v] {
			return fmt.Errorf("%s.settings_sources: %q is not a settings source (valid: user, project, local)", where, v)
		}
		if seen[v] {
			return fmt.Errorf("%s.settings_sources: %q appears twice", where, v)
		}
		seen[v] = true
	}
	return nil
}
