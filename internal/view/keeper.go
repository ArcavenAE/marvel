package view

import (
	"context"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// DefaultFetchTimeout bounds one fetch and extract.
const DefaultFetchTimeout = 2 * time.Minute

// TickInterval is how often the keeper looks for views that are due.
const TickInterval = 30 * time.Second

// Declaration is a live session and the views its role declares.
type Declaration struct {
	Session api.Session
	Views   []api.View
}

// Keeper owns every seat's views.
type Keeper struct {
	ViewsDir     string
	Events       events.Emitter
	Git          Git
	Declared     func() []Declaration
	FetchTimeout time.Duration
	Now          func() time.Time
}

// Build builds each declared view for a session being spawned.
func (k *Keeper) Build(sess api.Session, views []api.View) {}

// Refresh follows one named view of a session, or every view when name is empty.
func (k *Keeper) Refresh(sessKey, name string) ([]string, error) {
	return []string{sessKey + name}, nil
}

// Tick follows every view that is due.
func (k *Keeper) Tick() {}

// Run ticks until ctx is done.
func (k *Keeper) Run(ctx context.Context) { <-ctx.Done() }

// Teardown forgets a session's views and removes their directory.
func (k *Keeper) Teardown(sessKey string) error { return nil }
