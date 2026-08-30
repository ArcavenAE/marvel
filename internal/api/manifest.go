package api

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
}

// ManifestTeam is a team section of a manifest.
type ManifestTeam struct {
	Name   string          `toml:"name" yaml:"name"`
	Budget *ManifestBudget `toml:"budget,omitempty" yaml:"budget,omitempty"`
	Roles  []ManifestRole  `toml:"role"  yaml:"roles"`
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
	Name                 string               `toml:"name"                          yaml:"name"`
	Replicas             int                  `toml:"replicas"                      yaml:"replicas"`
	Runtime              ManifestRuntime      `toml:"runtime"                       yaml:"runtime"`
	RestartPolicy        string               `toml:"restart_policy,omitempty"      yaml:"restart_policy,omitempty"`
	MaxRestarts          int                  `toml:"max_restarts,omitempty"        yaml:"max_restarts,omitempty"`
	Permissions          string               `toml:"permissions,omitempty"         yaml:"permissions,omitempty"`
	DangerousPermissions bool                 `toml:"dangerous_permissions,omitempty" yaml:"dangerous_permissions,omitempty"`
	Persona              string               `toml:"persona,omitempty"             yaml:"persona,omitempty"`
	Identity             string               `toml:"identity,omitempty"            yaml:"identity,omitempty"`
	Policy               string               `toml:"policy,omitempty"              yaml:"policy,omitempty"`
	HealthCheck          *ManifestHealthCheck `toml:"healthcheck,omitempty"         yaml:"healthcheck,omitempty"`
	// ActivityTimeout is a duration string ("10m") opting this role into the
	// activity-staleness advisory (aae-orc-9box). Empty/unset disables it.
	// Parsed into Role.ActivityTimeout, the same string→duration shape as
	// healthcheck.timeout.
	ActivityTimeout string `toml:"activity_timeout,omitempty"    yaml:"activity_timeout,omitempty"`
}

// ManifestHealthCheck is the healthcheck section within a role.
type ManifestHealthCheck struct {
	Type             string `toml:"type"                         yaml:"type"`
	Timeout          string `toml:"timeout,omitempty"             yaml:"timeout,omitempty"`
	FailureThreshold int    `toml:"failure_threshold,omitempty"   yaml:"failure_threshold,omitempty"`
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
	return &m, nil
}

func unmarshalManifestTOML(data []byte) (*Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse toml manifest: %w", err)
	}
	return &m, nil
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
	for i, t := range m.Teams {
		if t.Name == "" {
			return nil, fmt.Errorf("parse manifest: team[%d].name is required", i)
		}
		if len(t.Roles) == 0 {
			return nil, fmt.Errorf("parse manifest: team[%d] must have at least one role", i)
		}
		for j, r := range t.Roles {
			if r.Name == "" {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].name is required", i, j)
			}
			if r.Replicas < 1 {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].replicas must be >= 1", i, j)
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
			// Permissions maps verbatim to --permission-mode; an empty
			// value means "unset" and is allowed, but a non-empty typo
			// silently breaks the session, so reject it here. Orthogonal
			// to dangerous_permissions (see canonicalPermissionModes).
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

// ValidateRuntimes checks that each role's runtime command (and script,
// if set) actually resolves on the daemon's host before the manifest
// is applied. Returns an aggregated error listing every missing binary
// so the operator sees all problems at once — not just the first.
//
// Resolution rules match exec.Command semantics:
//   - Absolute path ("/usr/local/bin/forestage"): os.Stat must succeed.
//   - Path with separator ("bin/simulator", "./scripts/x"): resolved
//     relative to the daemon CWD via os.Stat.
//   - Plain name ("sleep", "forestage"): exec.LookPath searches $PATH.
//   - Empty command: flagged; the manifest parser already catches this
//     but we defend here so misuse of the public API surfaces clearly.
//
// Scripts are checked as absolute/relative paths (never PATH-resolved)
// because scripts are typically repo-relative files, not executables.
//
// See ArcavenAE/marvel#9 / aae-orc-rjm — the pre-fix behavior was to
// silently create panes whose processes exited immediately, hiding the
// real error behind a downstream "can't find pane" warning.
func (m *Manifest) ValidateRuntimes() error {
	var missing []string
	for ti, t := range m.Teams {
		for ri, r := range t.Roles {
			ctx := fmt.Sprintf("team[%d=%s].role[%d=%s]", ti, t.Name, ri, r.Name)
			if err := validateCommand(r.Runtime.Command); err != nil {
				missing = append(missing, fmt.Sprintf("  %s: command %q: %v", ctx, r.Runtime.Command, err))
			}
			if r.Runtime.Script != "" {
				if err := validateScript(r.Runtime.Script); err != nil {
					missing = append(missing, fmt.Sprintf("  %s: script %q: %v", ctx, r.Runtime.Script, err))
				}
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("runtime pre-flight failed on %d role(s):\n%s", len(missing), strings.Join(missing, "\n"))
	}
	return nil
}

func validateCommand(cmd string) error {
	if cmd == "" {
		return errors.New("empty")
	}
	// Path — either absolute or contains a separator — must exist on disk.
	if filepath.IsAbs(cmd) || strings.ContainsRune(cmd, filepath.Separator) {
		if _, err := os.Stat(cmd); err != nil {
			return fmt.Errorf("not found: %w", err)
		}
		return nil
	}
	// Plain name — must resolve on $PATH.
	if _, err := exec.LookPath(cmd); err != nil {
		return fmt.Errorf("not on PATH: %w", err)
	}
	return nil
}

func validateScript(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("not found: %w", err)
	}
	return nil
}

// Apply converts a manifest into store resources and creates them.
func (m *Manifest) Apply(store *Store) error {
	now := time.Now().UTC()

	ws := &Workspace{Name: m.Workspace.Name, CreatedAt: now}
	// Ignore already-exists for workspace (idempotent apply).
	if err := store.CreateWorkspace(ws); err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("apply workspace: %w", err)
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
				Identity:             mr.Identity,
				Policy:               mr.Policy,
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
			roles = append(roles, role)
		}

		budget := mt.Budget.Budget()
		team := &Team{
			Name:       mt.Name,
			Workspace:  m.Workspace.Name,
			Roles:      roles,
			Budget:     budget,
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
