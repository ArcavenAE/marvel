package daemon

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/panemenu"
	"github.com/arcavenae/marvel/internal/runtime"
)

// paneMenuEvaluator is all the daemon keeps of the pane-menu source: it can run
// an evaluation and nothing else, so no daemon code can reassign the source's
// Capture or Hooks after the constructor below has set them.
type paneMenuEvaluator interface {
	Evaluate(now time.Time)
}

// paneMenuSource builds the pane-menu source (internal/panemenu) once. The
// package is handed two functions and no driver: Capture reads the visible
// screen with wrapped rows joined (capture-pane -J), and InFront applies the
// adapter's ForegroundRule to the pane's current command, so a harness is
// captured only where marvel's spawn record says it is the process in front.
// The daemon wiring of those two is the whole of what this file adds; the
// source itself cannot send a key.
func (d *Daemon) paneMenuSource() paneMenuEvaluator {
	d.paneMenuOnce.Do(func() {
		reg := runtime.NewRegistry()
		src := &panemenu.Source{
			Samples:  d.limitMenu,
			Store:    d.store,
			Readings: d.accounts,
			Events:   d.events,
			Capture:  d.driver.CapturePaneJoined,
			InFront: func(sess api.Session) bool {
				rule, ok := reg.Resolve(sess.Runtime.Name).(runtime.ForegroundRule)
				if !ok {
					return false
				}
				cmd, _, err := d.driver.PaneForeground(sess.PaneID)
				if err != nil {
					return false
				}
				_, front := rule.Foreground(cmd)
				return front
			},
			HostZone: d.hostLocation,
		}
		src.Hooks = d.newLimitAction().Hooks()
		d.paneMenu = src
	})
	return d.paneMenu
}

func (d *Daemon) evaluatePaneMenus(now time.Time) {
	d.paneMenuSource().Evaluate(now)
}
