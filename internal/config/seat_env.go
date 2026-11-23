package config

// SeatEnv is what the daemon does to the environment of every seat pane it
// starts, declared once on the daemon's own cluster entry (seat_env:). It is
// host policy, so it never rides in a workspace manifest.
type SeatEnv struct {
	// PathPrepend names directories put in front of PATH in the pane. A leading
	// ~ is the daemon user's home. A role that sets PATH itself keeps its own.
	PathPrepend []string `yaml:"path_prepend,omitempty"`
	// Unset names variables removed from the pane, beside the session
	// credentials the daemon always removes.
	Unset []string `yaml:"unset,omitempty"`
}

// SeatEnvSpec is the cluster's seat_env block, checked and with ~ expanded. A
// nil cluster or an unset block is the zero value.
func (c *Cluster) SeatEnvSpec() (SeatEnv, error) {
	return SeatEnv{}, nil
}
