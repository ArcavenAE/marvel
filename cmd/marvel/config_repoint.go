package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/knownhosts"
	"github.com/arcavenae/marvel/internal/paths"
)

// repointProbe reads the host key an address presents; a test replaces it so
// the command runs without a network.
var repointProbe = knownhosts.Probe

// repointCmd is `marvel config repoint`: move a cluster to a new address and
// carry its host key trust along, but only when the new address presents a key
// already recorded for the old one, and only after a person says yes.
func repointCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repoint <cluster> <address>",
		Short: "Move a cluster to a new address, carrying its host key trust over when the key matches",
		Long: `Move a cluster whose host changed address.

The command reads the host key recorded in ~/.marvel/known_hosts for the
cluster's current address, then reads the key the new address presents
without recording it. If the new address presents a key already recorded for
the old one, it prints both addresses and the fingerprint and asks before it
records the key for the new address and updates the cluster's address. The
cluster's identity and every other field stay as they are.

If the key differs, if no key is recorded for the old address, or if there is
no terminal to ask on, nothing is changed. A different key is a different
host: use 'marvel config add-cluster' and 'marvel keys trust' for that, which
ask in their own way.

The old address's entry in known_hosts is left in place.

Example:
  marvel config repoint prod mrvl://op@10.0.0.77:6785`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := cmd.InOrStdin()
			f, ok := in.(*os.File)
			interactive := ok && term.IsTerminal(int(f.Fd()))
			return runRepoint(cmd.OutOrStdout(), in, interactive, args[0], args[1])
		},
	}
}

// runRepoint moves cluster name to newAddr when the key newAddr presents is
// one known_hosts already holds for the cluster's current address. It writes
// nothing unless every check passes and the answer is yes.
func runRepoint(out io.Writer, in io.Reader, interactive bool, name, newAddr string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cl, err := cfg.GetCluster(name)
	if err != nil {
		return err
	}
	if name == "" || cl == nil {
		return fmt.Errorf("no cluster named %q", name)
	}
	oldHP, err := daemon.HostPortOf(cl.Server)
	if err != nil {
		return fmt.Errorf("cluster %q has no address to move from: %w", name, err)
	}
	newHP, err := daemon.HostPortOf(newAddr)
	if err != nil {
		return err
	}
	if cl.Server == newAddr {
		return fmt.Errorf("cluster %q already points at %s", name, newAddr)
	}
	layout, err := paths.Default()
	if err != nil {
		return err
	}

	recorded, err := knownhosts.Recorded(layout, oldHP)
	if err != nil {
		return err
	}
	if len(recorded) == 0 {
		return fmt.Errorf("no host key is recorded for %s, so there is nothing to carry over; "+
			"use 'marvel config add-cluster %s %s' and then 'marvel keys trust %s'", oldHP, name, newAddr, name)
	}

	presented, err := repointProbe(newHP)
	if err != nil {
		return fmt.Errorf("read the host key at %s: %w", newHP, err)
	}
	fp := ssh.FingerprintSHA256(presented)
	if !holdsKey(recorded, presented) {
		held := make([]string, 0, len(recorded))
		for _, k := range recorded {
			held = append(held, ssh.FingerprintSHA256(k))
		}
		return fmt.Errorf("the key at %s (%s) is not a key recorded for %s (%s); nothing was changed",
			newHP, fp, oldHP, strings.Join(held, ", "))
	}

	_, _ = fmt.Fprintf(out, "Cluster %q moves from %s to %s.\n", name, oldHP, newHP)
	_, _ = fmt.Fprintf(out, "The new address presents a key already recorded for the old one:\n  %s %s\n", presented.Type(), fp)
	if !interactive {
		return errors.New("config repoint needs a terminal to confirm; run it in one. Nothing was changed")
	}
	_, _ = fmt.Fprintf(out, "Record this key for %s and point %q at it? [y/N] ", newHP, name)
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read answer: %w", err)
	}
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return errors.New("not confirmed; nothing was changed")
	}

	already, err := knownhosts.Recorded(layout, newHP)
	if err != nil {
		return err
	}
	if !holdsKey(already, presented) {
		if err := knownhosts.Trust(layout, newHP, presented); err != nil {
			return err
		}
	}
	if err := cfg.AddCluster(name, newAddr, ""); err != nil {
		return err
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Cluster %q now points at %s.\n", name, newAddr)
	return nil
}

// holdsKey reports whether keys contains key.
func holdsKey(keys []ssh.PublicKey, key ssh.PublicKey) bool {
	for _, k := range keys {
		if bytes.Equal(k.Marshal(), key.Marshal()) {
			return true
		}
	}
	return false
}
