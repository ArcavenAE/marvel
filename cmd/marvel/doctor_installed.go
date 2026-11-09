package main

const (
	roleLibraryDirEnv = "MARVEL_ROLE_LIBRARY_DIR"
	roleLibraryJobEnv = "MARVEL_ROLE_LIBRARY_JOB"
)

// installSource names where the installed-copies records live. Each path is
// read, never written.
type installSource struct {
	RoleLibraryDir string
	RoleLibraryJob string
	DirectorHome   string
	UpdateLog      string
}

func checkInstalled(r *doctorReport, in doctorInput) {}

func hostInstallSource() installSource { return installSource{} }
