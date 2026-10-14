package runtime

import "errors"

// Red-first stubs: wrong on purpose until the tests fail for the right reason.

// ContainerSpec is everything the run command needs, passed in explicitly.
type ContainerSpec struct {
	Runtime  string
	Image    string
	Name     string
	WorkDir  string
	HomeDir  string
	EnvNames []string
	Command  string
}

// DetectContainerRuntime returns the first installed container runtime CLI.
func DetectContainerRuntime(lookPath func(string) (string, error)) (string, bool) {
	return "", false
}

// ContainerName builds a container name unique to one launch of one session.
func ContainerName(sessionKey, launch string) string { return "" }

// ContainerCommand builds the pane command that runs spec under its runtime.
func ContainerCommand(s ContainerSpec) (string, error) { return "", errors.New("not built") }
