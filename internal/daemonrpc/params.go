package daemonrpc

import "github.com/arcavenae/marvel/internal/events"

// The requests the daemon decodes, one type for each method that takes params.
// A client builds them and the daemon decodes them, so both name the same
// fields; the json tags are the wire, and a golden fixture under
// testdata/rpc pins each one (I-458 row 6).

// Logs params — tail of the daemon's in-memory log ring.
type LogsParams struct {
	N int `json:"n"` // number of lines; 0 or negative = unbounded (whole buffer)
}

// Events params — filtered tail of the daemon's structured event ring.
type EventsParams struct {
	N           int    `json:"n"` // number of events; <=0 returns the whole ring
	Workspace   string `json:"workspace,omitempty"`
	Team        string `json:"team,omitempty"`
	Role        string `json:"role,omitempty"`
	Session     string `json:"session,omitempty"`
	Kind        string `json:"kind,omitempty"`
	MinSeverity string `json:"min_severity,omitempty"` // "" or "warning"
	// SinceSeq returns only events with Seq strictly greater than this
	// value — the follow-mode resume cursor. Zero means no cursor.
	SinceSeq uint64 `json:"since_seq,omitempty"`
}

// Apply params
type ApplyParams struct {
	ManifestData []byte `json:"manifest_data"`
	// WorkspaceRoot is the absolute root marvel work resolved from the
	// manifest file's directory. When present it replaces the manifest's own
	// root, so the manifest bytes are posted untouched
	// (docs/design/session-working-directory.md, decisions 2 and 3).
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	// ManifestSource names where the manifest was applied from,
	// "<host>:<path>". The apply records it as the claim on every team the
	// file names; an older client sends none, and those teams take nil claims
	// (docs/design/work-prune.md section 3).
	ManifestSource string `json:"manifest_source,omitempty"`
	// Prune asks the apply to remove the teams this source put there and no
	// longer names, as the prune set computed after the apply stands
	// (docs/design/work-prune.md section 5).
	Prune bool `json:"prune,omitempty"`
}

// Get params
type GetParams struct {
	ResourceType string `json:"resource_type"`
	// Composer asks for the COMPOSER column, which reads each seat's pane.
	Composer bool `json:"composer,omitempty"`
}

// Describe params
type DescribeParams struct {
	ResourceType string `json:"resource_type"`
	Name         string `json:"name"`
}

// Delete params
type DeleteParams struct {
	ResourceType string `json:"resource_type"`
	Name         string `json:"name"`
}

// Scale params
type ScaleParams struct {
	TeamKey  string `json:"team_key"`
	Role     string `json:"role"`
	Replicas int    `json:"replicas"`
}

// ConvergeParams selects which team(s) to move and which way. An empty TeamKey
// targets every team (the daemon-wide go-line). Hold flips the posture the
// other way — back to the start line — for a future majordomo or an operator
// re-arming a team; the CLI exposes only the converge direction.
type ConvergeParams struct {
	TeamKey string `json:"team_key,omitempty"`
	Hold    bool   `json:"hold,omitempty"`
}

// Run params
type RunParams struct {
	Workspace      string   `json:"workspace"`
	Team           string   `json:"team"`
	Role           string   `json:"role"`
	RuntimeCommand string   `json:"runtime_command"`
	RuntimeArgs    []string `json:"runtime_args"`
	Script         string   `json:"script"`
	// WorkDir is where the session runs, absolute, from marvel run --workdir or
	// the caller's cwd. WorkDirDefault says the caller did not name it.
	WorkDir        string `json:"workdir,omitempty"`
	WorkDirDefault bool   `json:"workdir_default,omitempty"`
	// Mode is "headless" or empty (interactive). Prompt is the request a
	// headless run carries. Env is merged over the constructed environment.
	// Name fixes the session name, so the key is predictable; empty keeps the
	// clock-derived name.
	Mode   string            `json:"mode,omitempty"`
	Prompt string            `json:"prompt,omitempty"`
	Env    map[string]string `json:"env,omitempty"`
	Name   string            `json:"name,omitempty"`
}

// Shift params
type ShiftParams struct {
	TeamKey string `json:"team_key"`
	Role    string `json:"role,omitempty"`
	// NoHandoff shifts without asking the seat for the handoff its role
	// declares (marvel#1447).
	NoHandoff bool `json:"no_handoff,omitempty"`
}

// Reset-health params — clear one role's crash-loop state (RestartCount +
// backoff) without deleting the team. See aae-orc-fv3h.
type ResetHealthParams struct {
	TeamKey string `json:"team_key"` // workspace/team
	Role    string `json:"role"`
}

// Inject params — send keystrokes to a session's pane (executive privilege).
type InjectParams struct {
	SessionKey string `json:"session_key"`
	Text       string `json:"text"`
	Literal    bool   `json:"literal"`
	Enter      bool   `json:"enter"`
	// Injector is what the CLI declares about itself: the seat it runs in
	// and the user. It is a claim, not a proof; the daemon records it beside
	// the transport it saw itself.
	Injector Injector `json:"injector,omitempty"`
	// Verify reads the composer after the keys are sent and reports whether
	// the effect was seen. Off by default.
	Verify bool `json:"verify,omitempty"`
	// Clear discards a staged draft before the text is sent, only where the
	// harness's composer is known and a clear is safe.
	Clear bool `json:"clear,omitempty"`
	// SettleMS is how long verification waits for a redraw. Zero means the
	// default.
	SettleMS int `json:"settle_ms,omitempty"`
	// AllowBareDigit lifts one refusal: text starting with 1, 2 or 3 sent to a claude pane the
	// capture cannot rule a usage-limit menu out of (marvel#559 part b). It does
	// not lift the refusal on a menu the matcher finds.
	AllowBareDigit bool `json:"allow_bare_digit,omitempty"`
	// Ring marks the inject as a ring: it writes the session's ring claim
	// before the keys go in and is refused while another ring holds it
	// (docs/design/marvel-watcher.md, section 11.2). Off by default; an inject
	// without it ignores the claim.
	Ring bool `json:"ring,omitempty"`
}

// Capture params — read a session's pane content.
type CaptureParams struct {
	SessionKey string `json:"session_key"`
	Start      *int   `json:"start,omitempty"`
	End        *int   `json:"end,omitempty"`
	// Repaint asks the program in the pane to redraw before the read, by
	// widening the pane's terminal one column and restoring it. Off by
	// default: a plain capture is what was last painted.
	Repaint bool `json:"repaint,omitempty"`
	// SettleMS is how long the nudge is held, and then how long to wait for the
	// restore's redraw before reading. Zero means the default.
	SettleMS int `json:"settle_ms,omitempty"`
	// Escapes keeps the pane's escape sequences (capture-pane -e), so a reader
	// can tell dim text from typed text. Off by default: the output is what it
	// was.
	Escapes bool `json:"escapes,omitempty"`
	// Composer returns the composer state the session's reader gives, beside
	// the content. It types nothing. Like Repaint it nudges the pane's size,
	// because reading the composer repaints first.
	Composer bool `json:"composer,omitempty"`
}

// Stop params
type StopParams struct {
	// Teardown selects agent destruction over detach: delete every
	// session and kill every workspace tmux session before exiting.
	// Default (false) detaches: agents keep running and the next
	// daemon start adopts them.
	Teardown bool `json:"teardown,omitempty"`
	// KeepBus leaves a managed nats-server running when the daemon exits,
	// so the next daemon adopts it and the agents' bus connections hold.
	// Ignored with Teardown, which ends the bus with everything else.
	KeepBus bool `json:"keep_bus,omitempty"`
}

// BackendVerifyParams names one session to verify, or every session when
// blank.
type BackendVerifyParams struct {
	Session string `json:"session"`
}

type CredentialPutParams struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Audience string `json:"audience,omitempty"`
	Binding  string `json:"binding,omitempty"`
	// Value is the secret material. It rides the local unix socket or an
	// SSH-encrypted mrvl:// channel; it is stored in memory, then zeroed here
	// once the store holds its own copy, and never logged.
	Value []byte `json:"value"`
}

type CredentialNameParams struct {
	Name string `json:"name"`
}

// HibernateParams is the request. Teams names the teams, All takes every team,
// Backend and ExceptBackend select roles by label, End names one seat to end
// without a handoff, DryRun counts what would be signed off, and Status reads
// where each team stands.
type HibernateParams struct {
	Teams         []string `json:"teams,omitempty"`
	All           bool     `json:"all,omitempty"`
	Backend       []string `json:"backend,omitempty"`
	ExceptBackend []string `json:"except_backend,omitempty"`
	End           string   `json:"end,omitempty"`
	DryRun        bool     `json:"dry_run,omitempty"`
	Status        bool     `json:"status,omitempty"`
}

// WakeParams is the wake request.
type WakeParams struct {
	Teams      []string `json:"teams,omitempty"`
	All        bool     `json:"all,omitempty"`
	SkipChecks []string `json:"skip_checks,omitempty"`
	Order      []string `json:"order,omitempty"`
}

// SuspendParams is what the CLI sends with suspend: its own pid, so the
// collector leaves it and its ancestors out.
type SuspendParams struct {
	CLIPid int `json:"cli_pid"`
}

type ViewRefreshParams struct {
	Session string `json:"session"`
	Name    string `json:"name"`
}

// ReexecParams is what `reexec` may carry. ExecPath names the binary to exec
// instead of the daemon's own path, which is how an upgrade under an install
// that keeps each version in its own directory (mise) reaches the new build:
// the client that was just installed names itself (marvel#592).
type ReexecParams struct {
	ExecPath string `json:"exec_path,omitempty"`
}

// ExposureVerbParams names one slot and one function, the shape of all three
// exposure verbs. Paths and Caster belong to add; Reason to void (marvel#945).
type ExposureVerbParams struct {
	Workspace string   `json:"workspace"`
	Team      string   `json:"team"`
	Role      string   `json:"role"`
	Function  string   `json:"function"`
	Paths     []string `json:"paths,omitempty"`
	Caster    string   `json:"caster,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

// BusAuditParams names the record `marvel bus audit` reads.
type BusAuditParams struct {
	Seq uint64 `json:"seq"`
}

// Injector is the caller's own account of who it is.
type Injector struct {
	Session string `json:"session,omitempty"`
	User    string `json:"user,omitempty"`
}

// Filter is the event filter the params ask for.
func (p EventsParams) Filter() events.Filter {
	return events.Filter{
		Workspace:   p.Workspace,
		Team:        p.Team,
		Role:        p.Role,
		Session:     p.Session,
		Kind:        events.Kind(p.Kind),
		MinSeverity: events.Severity(p.MinSeverity),
		SinceSeq:    p.SinceSeq,
	}
}
