// Package knownhosts manages marvel's ~/.marvel/known_hosts file —
// trust-on-first-use host-key verification for mrvl:// connections.
//
// The file is an OpenSSH-format known_hosts database:
//
//	[host]:port ssh-ed25519 AAAA... comment
//
// On a first connection to a host that is not yet recorded, the callback
// returned by Callback will either prompt an interactive user to trust
// the presented key or return an error (for non-interactive use).
// Mismatches are always an error regardless of TTY state — that is the
// point of the file.
package knownhosts

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	xknownhosts "golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/term"

	"github.com/arcavenae/marvel/internal/paths"
)

// Mode controls what happens when a host is not yet known.
type Mode int

const (
	// ModePrompt asks an interactive user, falling back to ModeStrict
	// when there is no TTY.
	ModePrompt Mode = iota
	// ModeStrict refuses unknown hosts — use this for scripts and CI.
	ModeStrict
	// ModeTrust silently records any unknown host (dangerous; used by
	// the `marvel keys trust` command to bootstrap non-interactive use).
	ModeTrust
)

// Callback returns an ssh.HostKeyCallback for mrvl:// clients that
// verifies against layout.KnownHosts(), honouring mode for unknown
// hosts. The prompt writer and reader default to os.Stderr/os.Stdin
// when nil — override in tests.
func Callback(layout paths.Layout, mode Mode, prompt io.Writer, answer io.Reader) ssh.HostKeyCallback {
	path := layout.KnownHosts()
	if prompt == nil {
		prompt = os.Stderr
	}
	if answer == nil {
		answer = os.Stdin
	}

	// Ensure the file exists so xknownhosts.New doesn't fail.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := layout.EnsureHome(); err == nil {
			_ = os.WriteFile(path, nil, paths.ModeKnownHosts)
			_ = os.Chmod(path, paths.ModeKnownHosts)
		}
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		verify, err := xknownhosts.New(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}

		err = verify(hostname, remote, key)
		if err == nil {
			return nil
		}

		var keyErr *xknownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}

		if len(keyErr.Want) > 0 {
			// Mismatch — a key is recorded but doesn't match. Never
			// silently update.
			return fmt.Errorf(
				"host key for %s changed; offered %s (%s), expected %s — "+
					"possible MITM attack, investigate before continuing; "+
					"to accept the new key (only if you are certain), remove "+
					"the matching line from %s and reconnect",
				hostname,
				ssh.FingerprintSHA256(key), key.Type(),
				formatWant(keyErr.Want),
				path,
			)
		}

		// Unknown host — decide what to do based on mode.
		switch mode {
		case ModeStrict:
			return fmt.Errorf(
				"host key for %s is not trusted (SHA256:%s); run "+
					"'marvel keys trust <cluster>' or connect interactively to add it",
				hostname, fingerprintBytes(key),
			)
		case ModeTrust:
			return appendKnownHost(path, hostname, key)
		case ModePrompt:
			if isTTY(answer) {
				return promptAndTrust(path, hostname, key, prompt, answer)
			}
			return fmt.Errorf(
				"host key for %s is not trusted (SHA256:%s); run "+
					"'marvel keys trust <cluster>' to accept non-interactively",
				hostname, fingerprintBytes(key),
			)
		}
		return err
	}
}

// Trust unconditionally records host+key in known_hosts. Callers use
// this after performing their own verification (for example, the
// admin read out the fingerprint over voice or a previous channel).
func Trust(layout paths.Layout, hostname string, key ssh.PublicKey) error {
	return appendKnownHost(layout.KnownHosts(), hostname, key)
}

// appendKnownHost writes an OpenSSH-format entry to known_hosts.
func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	line := xknownhosts.Line([]string{xknownhosts.Normalize(hostname)}, key)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, paths.ModeKnownHosts)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintln(f, line); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	_ = os.Chmod(path, paths.ModeKnownHosts)
	return nil
}

func promptAndTrust(path, hostname string, key ssh.PublicKey, prompt io.Writer, answer io.Reader) error {
	_, _ = fmt.Fprintf(prompt, "\n")
	_, _ = fmt.Fprintf(prompt, "The authenticity of host %s can't be established.\n", hostname)
	_, _ = fmt.Fprintf(prompt, "  %s key fingerprint: SHA256:%s\n", key.Type(), fingerprintBytes(key))
	_, _ = fmt.Fprintf(prompt, "Compare with 'marvel keys host-fingerprint' on the daemon.\n")
	_, _ = fmt.Fprintf(prompt, "Trust this key and add it to %s? [y/N] ", path)

	buf := make([]byte, 16)
	n, err := answer.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read answer: %w", err)
	}
	resp := strings.TrimSpace(strings.ToLower(string(buf[:n])))
	if resp != "y" && resp != "yes" {
		return fmt.Errorf("host key rejected by user")
	}
	if err := appendKnownHost(path, hostname, key); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(prompt, "Added %s to %s.\n", hostname, path)
	return nil
}

// isTTY reports whether r is an interactive terminal. Uses x/term to
// distinguish real TTYs from character devices such as /dev/null.
func isTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func fingerprintBytes(key ssh.PublicKey) string {
	fp := ssh.FingerprintSHA256(key)
	return strings.TrimPrefix(fp, "SHA256:")
}

func formatWant(want []xknownhosts.KnownKey) string {
	if len(want) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(want))
	for _, k := range want {
		parts = append(parts, fmt.Sprintf("%s (%s)", ssh.FingerprintSHA256(k.Key), k.Key.Type()))
	}
	return strings.Join(parts, ", ")
}
