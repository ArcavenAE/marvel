package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/daemon"
)

// adoptsText is what 'marvel daemon reexec' does, said once for every place
// that points an operator at it: on the daemon's host it execs the binary the
// command itself runs from, which is the installed one after an upgrade.
const adoptsText = "'marvel daemon reexec', run on the daemon's host, adopts the marvel binary it is run from"

// isLocalDaemonAddr reports whether addr is the daemon's own unix socket, the
// only transport on which the daemon accepts an exec_path.
func isLocalDaemonAddr(addr string) bool { return addr != "" && !strings.Contains(addr, "://") }

// reexecRequest builds the reexec request. Against a local daemon it names the
// binary this command runs from, so an install that keeps each version in its
// own directory adopts the new build instead of restarting the old one
// (marvel#592). Against a remote daemon, or when this binary's path cannot be
// read, it sends none, and the daemon execs its own path as before. The second
// result is the resolved path that was sent, or "".
func reexecRequest() (daemon.Request, string) {
	req := daemon.Request{Method: "reexec"}
	addr, _, err := resolveDaemonAddr()
	if err != nil || !isLocalDaemonAddr(addr) {
		return req, ""
	}
	exe, err := os.Executable()
	if err != nil {
		return req, ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	params, err := json.Marshal(map[string]string{"exec_path": exe})
	if err != nil {
		return req, ""
	}
	req.Params = params
	return req, exe
}

// reexecNote says when the daemon did not exec the binary that was named: an
// older daemon ignores exec_path and re-executes its own path.
func reexecNote(resp *daemon.Response, sent string) string {
	if sent == "" {
		return ""
	}
	var r struct {
		Binary string `json:"binary"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil || r.Binary == "" || r.Binary == sent {
		return ""
	}
	return fmt.Sprintf("note: the daemon is re-executing %s, not %s; it predates exec_path, so it kept its own path", r.Binary, sent)
}
