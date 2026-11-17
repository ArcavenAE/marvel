package daemon

import "github.com/arcavenae/marvel/internal/config"

// Red-phase stubs: the gate is not implemented yet, so the tests in
// credential_reveal_gate_test.go fail on their assertions.

func credentialRevealEnabled(*config.Cluster) bool { return true }

func (d *Daemon) loadCredentialReveal(string) {}
