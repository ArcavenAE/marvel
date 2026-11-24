package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/session"
)

// run.sandboxed starts a command for a seat, outside the seat's own profile and
// under one profile the daemon composes (docs/design/seat-tcc-deny.md, sections
// 5 and 14; marvel#1596). The seat is identified by its heartbeat token and
// nothing else, and the daemon never runs anything unwrapped.

const (
	defaultRunTimeout = 10 * time.Minute
	maxRunTimeout     = time.Hour
	// sessionWatchEvery is how often a live run checks that its seat still runs.
	sessionWatchEvery = time.Second
)

// authorizeRun resolves the seat a run.sandboxed token belongs to. The request
// names no session, so the token is the identity; only a bound token of a
// running seat is admitted.
func (d *Daemon) authorizeRun(token string) (api.Session, error) {
	sess, err := d.store.SessionByHeartbeatToken(token)
	if err != nil {
		return api.Session{}, errors.New("run.sandboxed: not authorized")
	}
	live, err := d.store.AuthenticateLiveSession(sess.Key(), token)
	if err != nil {
		return api.Session{}, errors.New("run.sandboxed: not authorized")
	}
	return live, nil
}

// runLogRecord is the one log line for a run: who, what program, which profile
// by digest, and how it ended. It carries no environment value, no argument
// past the program name and no profile text.
func runLogRecord(sess api.Session, req api.RunSandboxedRequest, composed string, rc int, took time.Duration) string {
	sum := sha256.Sum256([]byte(composed))
	argv0 := ""
	if len(req.Argv) > 0 {
		argv0 = req.Argv[0]
	}
	return fmt.Sprintf("run.sandboxed seat=%s argv0=%s argc=%d profile_sha256=%s rc=%d took=%s",
		sess.Key(), argv0, len(req.Argv), hex.EncodeToString(sum[:]), rc, took)
}

// runChild runs argv in its own process group (session.RunChild). The process
// start lives in the session package, which already launches seats' commands,
// so the daemon package keeps its pinned list of process starts.
func runChild(ctx context.Context, argv []string, dir string, env []string, timeout time.Duration, maxOut int) (api.RunSandboxedResult, error) {
	return session.RunChild(ctx, argv, dir, env, timeout, maxOut)
}

// runCarveOut is the path the deny block lets back through a denied folder: the
// seat's recorded working directory, never the request's Dir.
func runCarveOut(sess api.Session, _ api.RunSandboxedRequest) []string {
	if sess.WorkDir == "" {
		return nil
	}
	return []string{sess.WorkDir}
}

// handleRunSandboxed serves run.sandboxed. ctx is the connection's: it ends
// when the seat hangs up. A live run also ends with its seat's session.
func (d *Daemon) handleRunSandboxed(ctx context.Context, params json.RawMessage) daemonrpc.Response {
	var req api.RunSandboxedRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	sess, err := d.authorizeRun(req.Token)
	if err != nil {
		return daemonrpc.Response{Error: err.Error()}
	}
	if len(req.Argv) == 0 || !filepath.IsAbs(req.Argv[0]) {
		return daemonrpc.Response{Error: "run.sandboxed: argv must name an absolute program path"}
	}
	goos, sbx := d.runGOOS, d.runSandboxExec
	if goos == "" {
		goos = runtime.GOOS
	}
	if sbx == "" {
		sbx = "/usr/bin/sandbox-exec"
	}
	if err := session.CheckRunHost(goos, sbx); err != nil {
		return daemonrpc.Response{Error: "run.sandboxed: no sandbox on this host, nothing was run: " + err.Error()}
	}
	team, err := d.store.GetTeam(sess.Workspace + "/" + sess.Team)
	if err != nil {
		return daemonrpc.Response{Error: "run.sandboxed: the seat's team is not known"}
	}
	var role api.Role
	found := false
	for _, r := range team.Roles {
		if r.Name == sess.Role {
			role, found = r, true
		}
	}
	if !found {
		return daemonrpc.Response{Error: "run.sandboxed: the seat's role is not known"}
	}
	roleText, err := session.RoleProfileText(role)
	if err != nil {
		return daemonrpc.Response{Error: "run.sandboxed: " + err.Error()}
	}
	// The request is checked on its own first, so a refusal does not depend on
	// the deny block being buildable here.
	if _, err := session.ComposeRunProfile(roleText, req.Profile, ""); err != nil {
		return daemonrpc.Response{Error: "run.sandboxed: " + err.Error()}
	}
	dir := req.Dir
	if dir != "" {
		resolved, _, derr := resolveRunWorkDir(dir, false)
		if derr != nil {
			return daemonrpc.Response{Error: "run.sandboxed: " + derr.Error()}
		}
		dir = resolved
	}
	if d.sessMgr == nil {
		return daemonrpc.Response{Error: "run.sandboxed: no session manager, nothing was run"}
	}
	plan, err := d.sessMgr.PrepareRun(sess, roleText, req)
	if err != nil {
		return daemonrpc.Response{Error: "run.sandboxed: " + err.Error()}
	}
	defer plan.Remove()

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}
	if timeout > maxRunTimeout {
		timeout = maxRunTimeout
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go d.watchRunSeat(runCtx, cancel, sess.Key(), req.Token)

	argv := append(append([]string{}, plan.Prefix...), req.Argv...)
	exec := d.runExec
	if exec == nil {
		exec = runChild
	}
	start := time.Now()
	res, err := exec(runCtx, argv, dir, req.Env, timeout, api.MaxRunOutputBytes)
	took := time.Since(start)
	if err != nil {
		log.Printf("%s", runLogRecord(sess, req, plan.Composed, -1, took)+" start_failed=true")
		return daemonrpc.Response{Error: "run.sandboxed: " + err.Error()}
	}
	log.Printf("%s", runLogRecord(sess, req, plan.Composed, res.ExitCode, took))
	return respond(res)
}

// watchRunSeat cancels a run when its seat's session is gone or replaced.
func (d *Daemon) watchRunSeat(ctx context.Context, cancel context.CancelFunc, key, token string) {
	t := time.NewTicker(sessionWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := d.store.AuthenticateLiveSession(key, token); err != nil {
				cancel()
				return
			}
		}
	}
}
