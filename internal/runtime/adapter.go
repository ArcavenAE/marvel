// Package runtime provides adapter interfaces and implementations for
// launching BYOA agent sessions. Each adapter knows how to construct the
// execution environment (command, args, env vars) for a specific runtime
// (forestage, bare claude CLI, or any generic command).
//
// A constructed environment can leak. Measured on Crush v0.88.1
// (finding-020): its server serves the client's entire process
// environment on GET /v1/workspaces, 76 entries including a database
// password, to any client of a host-shared socket. Environment
// construction is marvel's one built enforcement locus, so a harness that
// republishes it turns the enforcement surface into a disclosure surface.
// Adapters put identity, paths and flags in the environment; secrets
// belong somewhere a harness cannot serialize.
//
// No Crush adapter exists yet. When one is written it needs
// CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1 unconditionally, because without
// it every spawned session rewrites the host-global provider cache, and
// CRUSH_GLOBAL_DATA does not contain that write. Relocating
// CRUSH_GLOBAL_DATA is safe (caches and model selections);
// CRUSH_GLOBAL_CONFIG is the operator's credential store and is not.
//
// It also needs a stated position on crushrc. Crush executes a project
// directory's .crushrc as bash at config load, ahead of any permission
// list, and marvel workspaces are checkouts of arbitrary repositories.
// Measured firing on `crush run` with no trust prompt (finding-020 §9).
//
// Identity stamping: baseEnv sets BEADS_ACTOR for every session, so a
// marvel-managed agent writes the beads tracker as itself rather than
// as the human default. The spawner stamps attribution because a
// harness cannot derive its own identity from inside the pane (orc
// finding-123; callbook findings 005 and 006). The per-session value
// also beats any machine-global BEADS_ACTOR the pane would inherit
// from the operator's shell, which is the point: per-agent identity
// over one shared name.
package runtime

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/arcavenae/marvel/internal/api"
)

// LaunchContext holds the information an adapter needs to construct the
// execution environment for a session.
type LaunchContext struct {
	Session    *api.Session
	Role       *api.Role
	Team       *api.Team
	Workspace  *api.Workspace
	SocketPath string
	// BusURL, BusUser, and BusPassword come from the cluster's bus section
	// (brief 10 section 4, aae-orc-1qyo3). BusURL alone means an adopted
	// broker with no authorization; the shim then connects anonymously, as
	// it does today. User and password are the team credential the daemon
	// minted into the broker's authorization file; per-session users are a
	// follow-on on the same seam.
	BusURL      string
	BusUser     string
	BusPassword string
	// StreamPath is a sink the session manager created for this launch —
	// a FIFO the harness's structured output can be redirected into.
	// Empty means marvel is not observing this session's stream, either
	// because the adapter declined SupportsStream or because the manager
	// has no sink directory. An adapter that finds it empty must produce
	// a working command anyway.
	StreamPath string
	// HarnessSessionID is the session id marvel assigned this launch, for
	// an adapter that implements SessionIDAssigner. The session manager
	// mints it per launch and records it on the session before Prepare, so
	// the value the harness is told is the value marvel kept.
	//
	// Empty means this launch carries no assigned id, either because the
	// adapter does not implement SessionIDAssigner, or because it declined
	// for this launch, or because minting failed. An adapter that finds it
	// empty must produce a working command anyway, exactly as it does for
	// StreamPath.
	HarnessSessionID string
	// HarnessHomePath is the private state directory the session manager
	// created for this launch, for an adapter that implements
	// SessionHomeAssigner. Empty means this launch shares the operator's
	// own harness home, which is the behavior every session had before.
	// An adapter that finds it empty must produce a working command
	// anyway, exactly as it does for StreamPath.
	HarnessHomePath string
	// PolicyProjectionPath is the file the session manager wrote this
	// session's projected Claude Code settings fragment to (see
	// finding-024). It is non-empty only when the role references a policy
	// AND the adapter reported ProjectionFor().Supported. Prepare appends
	// the adapter's own read-flag pointing at it (claude: a top-level
	// --settings; forestage: --settings in the claude passthrough). Empty
	// means no projection for this launch and Prepare injects nothing.
	PolicyProjectionPath string
}

// ProjectionTarget is an adapter's answer to "where does this session's
// projected policy settings file go, and can this harness read one?".
// The session manager calls ProjectionFor at spawn and on every live
// re-projection; it writes Settings JSON to Path only when Supported.
type ProjectionTarget struct {
	// Supported reports whether this harness reads a settings fragment
	// from a path marvel chooses. False for harnesses that do not (codex,
	// opencode, generic today); marvel then logs the referenced policy as
	// advisory for that runtime rather than dropping it silently.
	//
	// The bar is deliberately "accepts a settings path as a launch
	// argument", not "has a settings file". A harness with a config file
	// but no way to be handed one can only be configured by writing into
	// the user's own config directory, where the blast radius is their
	// interactive use of that tool in every project and it outlives
	// marvel's uninstall. Returning false and logging the policy as
	// advisory is the correct answer for that harness.
	Supported bool
	// Path is the file marvel writes the resolved settings fragment to.
	// Set only when Supported, and it must be inside the dir passed to
	// ProjectionFor; the session manager refuses a path outside it.
	Path string
}

// SessionHomeSpec is an adapter's answer to "does this harness keep its
// whole mutable state under one relocatable directory, and what is needed
// in a private one for the harness to work?".
type SessionHomeSpec struct {
	// EnvVar is the environment variable that relocates the state tree,
	// e.g. CODEX_HOME. The adapter sets it in Prepare; naming it here lets
	// the manager report which lever it is honouring.
	EnvVar string
	// Source is the operator's own harness home, which a private home is
	// seeded from. Empty means nothing is seeded.
	Source string
	// LinkIn names entries under Source that the private home cannot work
	// without, and that are SYMLINKED rather than copied. Credentials are
	// the reason that distinction is written into the type: codex keeps
	// auth.json under its home, so a private home with no link to it is a
	// session that cannot authenticate. A symlink leaves the credential
	// the operator's, in their file, under their permissions, revoked by
	// deleting the original; a copy would make marvel a second holder of
	// bearer authority at a third party, which SOUL section 3 and ADR-009
	// place outside the custody boundary.
	//
	// A named entry that does not exist under Source is skipped, not an
	// error: an operator who has not logged that harness in yet is not a
	// spawn failure, they are a session that will report it themselves.
	LinkIn []string
	// Seed writes the files marvel AUTHORS into the private home, after the
	// links are in place. It is the positive allowlist beside LinkIn: a
	// harness whose config file mixes settings a session needs with the
	// operator's own trust decisions and identity cannot have that file
	// linked in, so marvel writes a file of its own instead, naming each
	// key it sets and nothing else (marvel#308). Nil means nothing is
	// written. An error is logged and the session still launches.
	Seed func(dir string) error
}

// SessionHomeAssigner is the optional containment contract. An adapter
// implements it when its harness keeps its entire mutable state under one
// directory that an environment variable relocates, so marvel can give a
// session a private one.
//
// This is the container half of "marvel assigns identity rather than
// discovering it" (aae-orc-ca7y), and it is the answer for a harness that
// offers no session-id pin. It does not name the session; it names the
// PLACE, which makes the session's own artifacts the only ones there.
// Measured on codex 0.153.4: a private CODEX_HOME holds exactly one
// rollout for the session that ran under it, so the pane-to-rollout
// binding is a path marvel chose rather than a file it has to hunt for.
//
// Optional for the same reason SessionIDAssigner is: a harness that
// scatters state, or that has no such lever, must not be handed a private
// directory it will half-use.
type SessionHomeAssigner interface {
	// SessionHome reports how to give this launch a private state home.
	// ok is false when this launch should share the operator's own home,
	// which is the pre-existing behavior for every runtime.
	SessionHome(ctx *LaunchContext) (spec SessionHomeSpec, ok bool)
}

// SessionIDAssigner is the optional identity contract. An adapter
// implements it when its harness accepts a session identifier chosen by
// the caller, which lets marvel NAME the session it is about to start
// rather than hunt for it afterwards (aae-orc-ca7y).
//
// It is an optional interface rather than a flag on Adapter for the same
// reason StreamCapable and StatuslineFeeder are: most of the roster offers
// no id pin, and a field every adapter had to answer would invite marvel
// to mint an id no harness is ever told, recording on the session an
// identity that does not exist anywhere else. Not implementing it is the
// correct answer for a harness that cannot be named, and it leaves that
// session exactly as it is today.
//
// The bar is "accepts an id as a launch argument and uses it", not "has a
// session id". codex mints its own and exposes CODEX_THREAD_ID to
// children, which reads like a pin and is not one: it is a value codex
// SETS, not one it reads (aae-orc-ca7y, measured twice). An adapter for a
// harness like that belongs on the container-assignment path instead, and
// must not implement this.
type SessionIDAssigner interface {
	// AssignsSessionID reports whether this launch can carry a
	// marvel-assigned session id. It is asked per launch, not once per
	// adapter, so a harness whose pin depends on mode or on the caller's
	// own args can decline the launches it cannot name.
	AssignsSessionID(ctx *LaunchContext) bool
}

// StatuslineFeeder is the optional half of the projection contract. An
// adapter implements it when its harness can express marvel's context
// feed as keys in its own settings schema, and the session manager asks
// the adapter for those keys rather than assuming any one harness's.
//
// Splitting it from Adapter keeps two things apart that a single
// Supported flag ran together: whether a harness reads a projected
// settings file at all, and whether it has somewhere to put a CTX% hook.
// A harness can do the first without the second, and an adapter that does
// not implement this gets no feed keys rather than another harness's.
type StatuslineFeeder interface {
	// StatuslineFeed renders the context feed in this harness's own
	// settings schema. command is the full shell command the hook must
	// run. The manager merges the returned keys into the projected
	// settings only where the policy has not already defined them, so a
	// policy always wins over the feed.
	StatuslineFeed(command string) map[string]any
}

// LaunchResult is what the adapter returns: the fully resolved command,
// arguments, and environment variables ready for the tmux driver.
type LaunchResult struct {
	Command string
	Env     map[string]string
	// Stream is set only when the adapter actually wired its harness's
	// structured output into LaunchContext.StreamPath. Nil means the
	// session produces no parseable stream and is observed by
	// capture-pane alone.
	Stream *StreamSpec
}

// Adapter knows how to prepare the execution environment for a specific
// runtime type. Adapters are stateless — all session-specific state is
// in the LaunchContext.
type Adapter interface {
	// Name returns the adapter identifier (e.g., "forestage", "claude", "generic").
	Name() string

	// Prepare constructs the command, args, and environment for launching
	// a session. The returned command string is passed to tmux new-window.
	Prepare(ctx *LaunchContext) (*LaunchResult, error)

	// ProjectionFor reports where marvel writes this session's projected
	// Claude Code settings fragment and whether the harness can read one.
	// dir is the manager-owned directory projected files live under. The
	// session manager calls this at spawn (before Prepare) and on every
	// live re-projection, so the answer must depend only on the runtime
	// and the launch identity, never on prior Prepare state. A harness
	// with no settings surface returns Supported=false.
	ProjectionFor(ctx *LaunchContext, dir string) ProjectionTarget
}

// Registry maps runtime names to adapters.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
	fallback Adapter
}

// NewRegistry creates a registry with the standard adapters pre-registered.
func NewRegistry() *Registry {
	r := &Registry{
		adapters: make(map[string]Adapter),
		fallback: &Generic{},
	}
	r.Register(&Forestage{})
	r.Register(&Claude{})
	r.Register(&Codex{})
	r.Register(&OpenCode{})
	r.Register(&Simulator{})
	r.Register(&Generic{})
	return r
}

// Register adds an adapter to the registry.
func (r *Registry) Register(a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Name()] = a
}

// Resolve finds the adapter for a runtime name. Falls back to the generic
// adapter for unknown runtimes — marvel manages any process in a tmux pane.
func (r *Registry) Resolve(runtimeName string) Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if a, ok := r.adapters[runtimeName]; ok {
		return a
	}
	return r.fallback
}

// baseEnv returns the environment variables common to all adapters: the
// values marvel constructs, then the role's declared env merged over them.
func baseEnv(ctx *LaunchContext) map[string]string {
	return mergeRoleEnv(ctx, constructedEnv(ctx))
}

// constructedEnv returns the values marvel itself stamps into every
// session, before any role override. It is its own function because the
// set of names is also a contract: a harness that does not pass its
// environment on to its tool children (codex, for MCP servers) has to be
// told which names to forward, and this is that list.
func constructedEnv(ctx *LaunchContext) map[string]string {
	env := map[string]string{
		"MARVEL_SESSION":   ctx.Session.Name,
		"MARVEL_ROLE":      ctx.Role.Name,
		"MARVEL_TEAM":      ctx.Team.Name,
		"MARVEL_WORKSPACE": ctx.Workspace.Name,
		// DIRECTOR_AGENT_ID is the bus id the director-mcp shim reads. This
		// is the R-84 S0 fallback: the computed session name is deterministic
		// and unique per session, so a session reaches the bus with a working
		// id and no manifest surface. A declared name is the FORWARD identity,
		// and it wins here through the same override seam BEADS_ACTOR uses
		// below. The value tracks the session across an index change, so a
		// shift or restart mints a new id by design; a stable operator-chosen
		// id belongs to the FORWARD declared-name path.
		"DIRECTOR_AGENT_ID": ctx.Session.Name,
		// The rest of the bus address. The shim defaults both to
		// "default", which puts a seat outside its team user's
		// permissions (agent.<workspace>.<team>.>), so a seat whose
		// launcher did not set them by hand never reached its own
		// subjects (aae-orc-z5wuq, marvel finding-051 step 5).
		"DIRECTOR_TEAM":      ctx.Team.Name,
		"DIRECTOR_WORKSPACE": ctx.Workspace.Name,
		// The same identity, stamped for the beads tracker. Session
		// names are <team>-<role>-g<generation>-<index>, so the value
		// is deterministic and unique per agent session. The map is
		// the override seam: an adapter, or a future manifest env
		// surface, that sets its own value after baseEnv returns wins.
		// The rig column is deliberately not set here; its semantics
		// are gated on the bd HEAD survey (aae-orc-usohe).
		"BEADS_ACTOR": "marvel/" + ctx.Workspace.Name + "/" + ctx.Session.Name,
	}
	if ctx.BusURL != "" {
		// The bus is cluster topology, so it rides from the cluster config
		// the way MARVEL_SOCKET does, and a manifest names no broker.
		env["NATS_URL"] = ctx.BusURL
		if ctx.BusUser != "" {
			env["DIRECTOR_NATS_USER"] = ctx.BusUser
			env["DIRECTOR_NATS_PASS"] = ctx.BusPassword
		}
	}
	if ctx.SocketPath != "" {
		env["MARVEL_SOCKET"] = ctx.SocketPath
		// The socket is what makes a heartbeat possible, so the token
		// travels with it and a session that cannot reach the daemon
		// carries no secret. This is enforcement locus 1: marvel
		// constructs the environment, and what the agent can prove about
		// itself is what marvel put there.
		if ctx.Session.HeartbeatToken != "" {
			env[api.HeartbeatTokenEnv] = ctx.Session.HeartbeatToken
		}
	}
	return env
}

// mergeRoleEnv merges the role's declared env over the constructed values.
func mergeRoleEnv(ctx *LaunchContext, env map[string]string) map[string]string {
	// The per-role manifest Env is the override seam (Layer B): it is merged
	// LAST so a declared value wins over marvel's constructed defaults, which
	// is what the BEADS_ACTOR comment above promises a "future manifest env
	// surface". This is the per-role backend declaration
	// (design-backend-swaps.md); it is advisory against a settings-managed
	// host, so the reliable enforcement is the --settings overlay.
	//
	// Two classes never pass, because neither is a value seam:
	//
	//   1. The credentials that decide WHO this session is. The heartbeat
	//      token and the broker user and password are how marvel and the bus
	//      establish that a session is the agent it claims to be. A manifest
	//      field able to set them is not an override, it is a privilege
	//      escalation: a role could write presence under another agent's
	//      address and have every message attributed to it. This is
	//      enforcement locus 1, named above, so it cannot be a passthrough.
	//   2. A literal bearer. A constructed environment is readable by
	//      anything the harness republishes (finding-020), which makes it the
	//      most exposed surface marvel owns, and ADR-009 puts third-party
	//      authority outside the custody line entirely.
	//
	// Everything else a role declares still wins, including the identity
	// values it is merely inadvisable to change, because the operator owns
	// that choice. It is logged so that a wrong identity on the bus is
	// traceable to the manifest that caused it rather than being invisible.
	// Only the KEY is ever logged; a refused value is never echoed.
	for k, v := range ctx.Role.Runtime.Env {
		switch {
		case roleEnvRefused(k):
			log.Printf("warning: session %s: role env %q refused, marvel owns the credentials that prove a session's identity", ctx.Session.Name, k)
			continue
		case api.BackendBearerEnv(k):
			log.Printf("warning: session %s: role env %q refused, it carries a literal credential; declare a helper pointer instead (ADR-009)", ctx.Session.Name, k)
			continue
		}
		if _, constructed := env[k]; constructed {
			log.Printf("warning: session %s: role env %q overrides a marvel-constructed value", ctx.Session.Name, k)
		}
		env[k] = v
	}
	return env
}

// roleEnvRefusedKeys are the constructed variables a role may never override.
// Each one is a credential marvel or the broker checks to decide whether a
// session is who it says it is, which is why the answer is refusal rather than
// a warning: there is no legitimate manifest reason to set one, and the
// failure it enables is silent.
var roleEnvRefusedKeys = []string{
	api.HeartbeatTokenEnv,
	"DIRECTOR_NATS_USER",
	"DIRECTOR_NATS_PASS",
}

// roleEnvRefused reports whether a role-declared env key is one marvel refuses
// to let a manifest set.
func roleEnvRefused(k string) bool {
	for _, r := range roleEnvRefusedKeys {
		if r == k {
			return true
		}
	}
	return false
}

// buildCommand joins a binary and its args into the single command string
// that tmux new-window expects.
func buildCommand(binary string, args []string) string {
	cmd := binary
	for _, arg := range args {
		cmd += " " + shellQuote(arg)
	}
	return cmd
}

// shellQuote wraps an argument in single quotes if it contains spaces or
// shell metacharacters. Empty strings become ”.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, c := range s {
		if c == ' ' || c == '\'' || c == '"' || c == '\\' || c == '$' ||
			c == '`' || c == '!' || c == '&' || c == '|' || c == ';' ||
			c == '(' || c == ')' || c == '{' || c == '}' || c == '<' ||
			c == '>' || c == '*' || c == '?' || c == '[' || c == ']' ||
			c == '#' || c == '~' {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	// Single-quote the string, escaping embedded single quotes.
	result := "'"
	for _, c := range s {
		if c == '\'' {
			result += `'\''`
		} else {
			result += string(c)
		}
	}
	result += "'"
	return result
}

// settingsProjectionPath is the per-session projected settings file for
// adapters that read a Claude Code settings fragment (claude, forestage).
// One shape, shared, so the two supporting adapters agree on where the
// manager writes and re-writes the file. Session keys contain a slash
// (workspace/name); it is folded to a dash so the key is one path segment.
func settingsProjectionPath(dir, sessionKey string) string {
	name := strings.ReplaceAll(sessionKey, "/", "-") + ".settings.json"
	return filepath.Join(dir, name)
}

// resolveCommand returns the command to execute. If Runtime.Command is set,
// use it. Otherwise fall back to the runtime name (image) as the binary name.
func resolveCommand(rt *api.Runtime) string {
	if rt.Command != "" {
		return rt.Command
	}
	return rt.Name
}

// redirectStdout appends a stdout redirection to a command string. Safe
// because tmux runs a single-argument shell-command through `sh -c`,
// which is the same property buildCommand's quoting already relies on.
// stderr deliberately stays in the pane: it is the operator's window
// into a harness that failed before it produced any structured output.
func redirectStdout(cmd, path string) string {
	return cmd + " > " + shellQuote(path)
}

// redirectStdin appends a stdin redirection to a command string. A harness
// launched in a tmux pane inherits the pane's tty as stdin; a harness that
// reads stdin (codex exec appends piped stdin to its prompt, opencode run
// likewise) blocks forever on a tty nobody types into. Redirecting from
// /dev/null closes that mouth. Applied before redirectStdout so the two
// compose as `cmd < /dev/null > sink`.
func redirectStdin(cmd, path string) string {
	return cmd + " < " + shellQuote(path)
}

// ErrNoCommand is returned when a runtime has no command or image specified.
var ErrNoCommand = fmt.Errorf("runtime has no command or image")

// ErrNoPrompt is returned when a headless launch carries no prompt. A
// prompt-less headless harness reads stdin and hangs on a pane tty
// nobody is typing into, so this is a manifest error, not a default.
var ErrNoPrompt = fmt.Errorf("headless runtime has no prompt")
