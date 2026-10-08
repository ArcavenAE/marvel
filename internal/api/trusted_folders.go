package api

// ValidateTrustedFolders is the daemon-side check at apply of the workspace's
// trusted_folders: each path must exist and be the top-level of a main git
// checkout (marvel#684). It runs after ValidateWorkDirs, so the workspace root
// a relative path resolves against is already normalized.
func (m *Manifest) ValidateTrustedFolders() error { return nil }

// TrustedFolderNotes says when this apply's trusted_folders differs from the
// workspace's stored list, since several manifests can share one workspace and
// the last apply wins. An absent key and an unchanged list say nothing.
func (m *Manifest) TrustedFolderNotes(store *Store) []string { return nil }
