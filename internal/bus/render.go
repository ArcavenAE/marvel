// Package bus renders and maintains the configuration of a cluster's local
// NATS broker: nats-server.conf and the 0600 authorization.conf beside it
// (brief 10 sections 3 and 5, aae-orc-e9g8i). It writes files and mints
// passwords; it does not start a process. Supervision is a later seam
// (aae-orc-xy1dh) that plugs in through Reloader.
package bus

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

const (
	// ConfName and AuthName are the two rendered files, side by side. The conf
	// includes the auth file by this relative name, which nats-server resolves
	// against the conf's own directory.
	ConfName = "nats-server.conf"
	AuthName = "authorization.conf"

	// AdminUser is marvel's own broker identity: provisioning and break-glass,
	// never handed to a session.
	AdminUser = "marvel_admin"

	// LeafSeedEnv is the environment variable the leaf remote reads its nkey
	// seed from. It is referenced unquoted and whole-value in the conf, the
	// one form nats-server substitutes from the environment and refuses to
	// start on when unset (director finding on nats.conf variable forms).
	LeafSeedEnv = "DIRECTOR_LEAF_NKEY"

	// monitorPortOffset places the loopback monitoring port at listen port
	// plus this, the convention the hub already uses (4242 to 8242; D5).
	monitorPortOffset = 4000
)

// Spec is everything the renderer needs. Every name in it is a subject token
// already, or Render refuses.
type Spec struct {
	Domain    string // the cluster name; becomes the JetStream domain
	Listen    string // explicit host:port
	StoreDir  string // JetStream store root; the store itself is StoreDir/store
	HubURL    string // empty for a local-only cluster
	HubCAFile string // trusts a TLS hub from the leaf remote; empty for plaintext
	LeafSeed  bool   // a bus/leaf credential is in the Store; the leaf block renders only then
	Admin     User
	Seat      *SeatUser // the director seat, when the cluster declares one
	Teams     []TeamUser
}

// User is a broker user with a marvel-issued password.
type User struct {
	Name     string
	Password string
}

// TeamUser is one applied team's broker identity, confined to its own
// workspace subtree. Supervisor marks the team that holds a supervisor role;
// with a hub it also gets the global grants.
type TeamUser struct {
	Workspace  string
	Team       string
	Password   string
	Supervisor bool
}

// MonitorAddr returns the loopback monitoring address for a listen address:
// 127.0.0.1 at the listen port plus 4000. Loopback only, whatever host the
// broker listens on, because /leafz and /varz are for the daemon.
func MonitorAddr(listen string) (string, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("listen %q is not host:port: %w", listen, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n+monitorPortOffset > 65535 {
		return "", fmt.Errorf("listen %q: port cannot carry the monitoring offset of %d", listen, monitorPortOffset)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(n+monitorPortOffset)), nil
}

// validToken is the R-76 closed class [A-Za-z0-9_-], the shape the director
// shim and config.ValidateClusterName enforce. A workspace or team name
// outside it would build a subject that is not the team's; it is rejected,
// never rewritten.
func validToken(kind, s string) error {
	if s == "" {
		return fmt.Errorf("%s is empty", kind)
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return fmt.Errorf("%s %q contains %q; the class is [A-Za-z0-9_-] and a name is rejected, not rewritten (R-76)", kind, s, string(r))
		}
	}
	return nil
}

// RenderConf renders nats-server.conf. The leafnodes block renders only when
// both a hub URL and a leaf seed exist: a hub with no seed starts local-only
// and the daemon says so (section 6, S6).
func RenderConf(s Spec) (string, error) {
	if err := validToken("cluster name", s.Domain); err != nil {
		return "", err
	}
	if s.Listen == "" || s.StoreDir == "" {
		return "", errors.New("listen and store_dir are required")
	}
	monitor, err := MonitorAddr(s.Listen)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Rendered by marvel for cluster %s. Edit the bus section of\n", s.Domain)
	fmt.Fprintf(&b, "# ~/.marvel/config.yaml instead; this file is regenerated.\n\n")
	fmt.Fprintf(&b, "listen: %s\n", s.Listen)
	fmt.Fprintf(&b, "http: %s\n\n", monitor)
	fmt.Fprintf(&b, "jetstream {\n  store_dir: %q\n  domain: %s\n}\n\n", filepath.Join(s.StoreDir, "store"), s.Domain)
	fmt.Fprintf(&b, "include %q\n", AuthName)
	if s.HubURL != "" && s.LeafSeed {
		// nkey is unquoted on purpose: quoted, nats-server would take the
		// text "$DIRECTOR_LEAF_NKEY" literally and the leaf would fail auth
		// with no hint why; unquoted, an unset variable refuses to start.
		tls := ""
		if s.HubCAFile != "" {
			tls = fmt.Sprintf(", tls { ca_file: %q }", s.HubCAFile)
		}
		fmt.Fprintf(&b, "\nleafnodes {\n  remotes: [\n    { urls: [%q], nkey: $%s%s }\n  ]\n}\n", s.HubURL, LeafSeedEnv, tls)
	}
	return b.String(), nil
}

// RenderAuth renders authorization.conf from the declared principal set:
// the admin user, the director seat user when a seat is declared, and one
// confined user per applied team with the supervisor team's global grants
// when a hub is set. Every validation the file depends on (token class,
// duplicate team names across workspaces, reserved names) lives in
// DeclaredPrincipals.
func RenderAuth(s Spec) (string, error) {
	if err := validToken("cluster name", s.Domain); err != nil {
		return "", err
	}
	principals, err := DeclaredPrincipals(s)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Rendered by marvel for cluster %s; regenerated on apply. Mode 0600.\n", s.Domain)
	fmt.Fprintf(&b, "# One user per applied team, confined to agent.<workspace>.<team>.> (director#4 shape).\n\n")
	b.WriteString("authorization {\n  users: [\n")
	for _, p := range principals {
		if p.Scope == ScopeService && p.Name == s.Admin.Name {
			// Admin: marvel's own identity, publish and subscribe everything.
			fmt.Fprintf(&b, "    { user: %s, password: %q,\n      permissions { publish { allow: [ \">\" ] }, subscribe { allow: [ \">\" ] } } }\n", p.Name, p.Password)
			continue
		}
		fmt.Fprintf(&b, "    { user: %s, password: %q,\n      permissions {\n        publish   { allow: [ %s ] }\n        subscribe { allow: [ %s ] }\n      } }\n",
			p.Name, p.Password, quoteList(p.Publish), quoteList(p.Subscribe))
	}
	b.WriteString("  ]\n}\n")
	return b.String(), nil
}

// quoteList renders subjects as a quoted, comma-separated list. Quoting
// matters: an unquoted $JS.API.> would be read as a variable reference.
func quoteList(items []string) string {
	var b bytes.Buffer
	for i, it := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(it))
	}
	return b.String()
}

// NewPassword mints a broker password: 32 random bytes, URL-safe base64, so
// it is inside the token class and needs no escaping anywhere it travels.
func NewPassword() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mint password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// WriteFiles writes the two rendered files under dir, 0700 for the directory
// and 0600 for both files (paths.ModePrivate; the auth file holds passwords),
// and reports whether anything changed. An unchanged file is not rewritten,
// the same contract as policy projection, so a no-op apply is a no-op reload.
func WriteFiles(dir, conf, auth string) (bool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("create bus config dir %s: %w", dir, err)
	}
	changed := false
	for name, body := range map[string]string{ConfName: conf, AuthName: auth} {
		path := filepath.Join(dir, name)
		existing, err := os.ReadFile(path)
		if err == nil && bytes.Equal(existing, []byte(body)) {
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return changed, fmt.Errorf("read %s: %w", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return changed, fmt.Errorf("write %s: %w", path, err)
		}
		// Enforce the mode when the file pre-existed with a looser one.
		_ = os.Chmod(path, 0o600)
		changed = true
	}
	return changed, nil
}
