// Package team implements the team controller — a reconciliation loop
// that maintains desired replica count for each team.
package team

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/arcaven/marvel/internal/api"
	"github.com/arcaven/marvel/internal/session"
)

// Controller reconciles desired team state with actual running sessions.
type Controller struct {
	store      *api.Store
	sessMgr    *session.Manager
	SocketPath string
	mu         sync.Mutex
}

// NewController creates a team controller.
func NewController(store *api.Store, sessMgr *session.Manager) *Controller {
	return &Controller{store: store, sessMgr: sessMgr}
}

// ReconcileOnce runs one reconciliation pass for all teams.
func (c *Controller) ReconcileOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()

	teams := c.store.ListTeams()
	for _, t := range teams {
		c.reconcileTeam(t)
	}
}

func (c *Controller) reconcileTeam(t *api.Team) {
	current := c.store.ListSessionsByTeam(t.Workspace, t.Name)
	desired := t.Replicas
	actual := len(current)

	if actual < desired {
		for i := actual; i < desired; i++ {
			name := fmt.Sprintf("%s-%d", t.Name, c.nextIndex(t))
			rt := t.Runtime
			rt.Args = c.injectIdentity(rt.Args, name, t, rt.Script)
			sess := &api.Session{
				Name:      name,
				Workspace: t.Workspace,
				Team:      t.Name,
				Runtime:   rt,
			}
			if err := c.sessMgr.Create(sess); err != nil {
				log.Printf("reconcile: create session %s: %v", name, err)
			}
		}
	} else if actual > desired {
		excess := actual - desired
		for i := 0; i < excess; i++ {
			sess := current[len(current)-1-i]
			if err := c.sessMgr.Delete(sess.Key()); err != nil {
				log.Printf("reconcile: delete session %s: %v", sess.Key(), err)
			}
		}
	}
}

// nextIndex finds the next available index for a team's sessions.
func (c *Controller) nextIndex(t *api.Team) int {
	current := c.store.ListSessionsByTeam(t.Workspace, t.Name)
	max := -1
	for _, s := range current {
		// Parse index from name like "workers-3"
		var idx int
		if _, err := fmt.Sscanf(s.Name, t.Name+"-%d", &idx); err == nil {
			if idx > max {
				max = idx
			}
		}
	}
	return max + 1
}

// injectIdentity appends identity and script flags for runtimes that support them.
// Runtimes with a script or a team role get identity flags injected.
func (c *Controller) injectIdentity(args []string, name string, t *api.Team, script string) []string {
	if t.Role == "" && script == "" {
		return args
	}
	injected := make([]string, len(args))
	copy(injected, args)
	if script != "" {
		injected = append(injected, "--script", script)
	}
	injected = append(injected,
		"--name", name,
		"--workspace", t.Workspace,
		"--team", t.Name,
	)
	if c.SocketPath != "" {
		injected = append(injected, "--socket", c.SocketPath)
	}
	return injected
}

// Run starts the reconciliation loop, reconciling every interval.
// Blocks until ctx is cancelled.
func (c *Controller) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Reconcile immediately on start.
	c.ReconcileOnce()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.ReconcileOnce()
		}
	}
}
