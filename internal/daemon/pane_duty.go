package daemon

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// firstTurnDutyTTL is how long a first-turn observation holds. It is an owner
// reading, named once here so a review can change it in one place.
const firstTurnDutyTTL = 24 * time.Hour

// recordFirstTurns writes the duty.first-turn cell from the pane pass. It does
// nothing yet.
func (d *Daemon) recordFirstTurns(sessions []api.Session, now time.Time) {}
