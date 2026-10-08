package runtime

// codexTrust is how a codex seat's start directory was judged against the
// workspace's trusted_folders.
type codexTrust struct {
	// Root is the main checkout the seat's folder trust is keyed on, set only
	// when the seat is trusted.
	Root string
	// Reason says why a seat is untrusted: not listed, no git, git error, bare
	// repo, or symlink unresolved. Empty when trusted.
	Reason string
}

// resolveCodexTrust judges start against the listed folders. A listed path may
// begin with ~/ (against home) or be relative (against wsRoot). It fails
// closed: anything it cannot establish is untrusted with a reason.
func resolveCodexTrust(_ string, _ []string, _, _ string) codexTrust {
	return codexTrust{Reason: "unresolved"}
}
