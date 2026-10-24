package api

import "time"

// ManifestCadence is one [[team.role.cadence]] entry: a periodic prompt for an
// interactive seat (docs/design/declared-cadence.md V1). The durations are
// strings ("5m"), parsed when the manifest is applied.
type ManifestCadence struct {
	Name             string `toml:"name"               yaml:"name"`
	Schedule         string `toml:"schedule"           yaml:"schedule"`
	Timezone         string `toml:"timezone"           yaml:"timezone"`
	Prompt           string `toml:"prompt"             yaml:"prompt"`
	StartingDeadline string `toml:"starting_deadline"  yaml:"starting_deadline"`
	Grace            string `toml:"grace"              yaml:"grace"`
}

// Cadence is a validated cadence, carried on Role.Cadence. marvel serves the
// cadence it applied, never a file on disk.
type Cadence struct {
	Name             string        `json:"name"`
	Schedule         string        `json:"schedule"`
	Timezone         string        `json:"timezone"`
	Prompt           string        `json:"prompt"`
	StartingDeadline time.Duration `json:"starting_deadline"`
	Grace            time.Duration `json:"grace"`
}
