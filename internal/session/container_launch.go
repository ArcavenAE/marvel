package session

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

// The launch half of a container placement (docs/design/workspace-sandbox-shape.md,
// sections 5 and 7). Placement resolves per session from the workspace's explicit
// value, then the harness's recommendation, then localhost. A session that
// resolves to container runs its prepared command under the container runtime
// with its role's container_image. A role with no image is refused and given no
// default (operator ruling 2026-10-14), and nothing here starts a container-placed
// seat on the host.
//
// With no container runtime (docker or podman) on PATH a container placement is
// refused too, with a message that says what to install and that an explicit
// placement = "localhost" runs on the host (operator ruling 2026-10-14, "7.2
// refuse"). The refusal is the same shape as the missing image: no session
// record, a standing record with its own reason, one event per change, and it
// ends on its own when a runtime appears.

// containerRefusalSuffix keys the container refusal apart from the directory
// refusal of the same role, so each keeps its own record and neither clears the
// other.
const containerRefusalSuffix = "#container"

func (m *Manager) lookPath() func(string) (string, error) {
	if m.LookPath != nil {
		return m.LookPath
	}
	return exec.LookPath
}

// containerPlacement resolves where sess runs. It returns the role's image and
// true when the placement is container, and false when it is not. A container
// placement with no image returns an error wrapping ErrPlacementRefused, after
// recording the refusal, and so does a container placement with no container
// runtime; an image that is not a plain reference returns a plain error.
// Nothing is created in any of those cases.
func (m *Manager) containerPlacement(sess *api.Session) (string, bool, error) {
	roleKey := sess.Workspace + "/" + sess.Team + "/" + sess.Role
	key := roleKey + containerRefusalSuffix

	var explicit api.Placement
	if ws, err := m.store.GetWorkspace(sess.Workspace); err == nil {
		explicit = ws.Placement
	}
	placement, err := api.ResolvePlacement(explicit, m.adapters.RecommendedPlacement(sess.Runtime.Name))

	m.refusedMu.Lock()
	defer m.refusedMu.Unlock()
	if err != nil {
		return "", false, fmt.Errorf("session %s: %w", sess.Key(), err)
	}
	if placement != api.PlacementContainer {
		delete(m.refused, key)
		return "", false, nil
	}

	var image string
	if team, terr := m.store.GetTeam(sess.Workspace + "/" + sess.Team); terr == nil {
		for _, r := range team.Roles {
			if r.Name == sess.Role {
				image = r.ContainerImage
				break
			}
		}
	}
	if image == "" {
		msg := fmt.Sprintf("not started: %s/%s resolves to a container placement and role %q names no container_image; no default image is used (set container_image on the role, or placement = \"localhost\" on the workspace)", sess.Team, sess.Role, sess.Role)
		m.noteRefusal(sess, key, RefusalContainerImage, events.KindSessionContainerRefused, "", msg)
		return "", true, fmt.Errorf("refuse to start session %s: container placement with no container_image: %w", sess.Key(), ErrPlacementRefused)
	}
	if !api.ValidContainerImage(image) {
		delete(m.refused, key)
		return "", true, fmt.Errorf("session %s: container_image %q is not a plain image reference", sess.Key(), image)
	}
	if _, ok := runtime.DetectContainerRuntime(m.lookPath()); !ok {
		msg := fmt.Sprintf("not started: %s/%s resolves to a container placement and no container runtime (docker or podman) is on PATH; nothing is started on the host (install one, or set placement = \"localhost\" on the workspace)", sess.Team, sess.Role)
		m.noteRefusal(sess, key, RefusalContainerRuntime, events.KindSessionContainerRefused, "", msg)
		return "", true, fmt.Errorf("refuse to start session %s: container placement and no container runtime (docker or podman) on PATH: %w", sess.Key(), ErrPlacementRefused)
	}
	delete(m.refused, key)
	return image, true, nil
}

// wrapInContainer rewrites the launch plan's command to run under the container
// runtime. The environment stays the pane's own, and every name in it is
// forwarded by name only, so no value reaches a command line.
func (m *Manager) wrapInContainer(sess *api.Session, plan *launchPlan, image string) error {
	rt, ok := runtime.DetectContainerRuntime(m.lookPath())
	if !ok {
		return fmt.Errorf("no container runtime (docker or podman) is installed; not started on the host")
	}
	names := make([]string, 0, len(plan.env))
	for name := range plan.env {
		names = append(names, name)
	}
	sort.Strings(names)
	cmd, err := runtime.ContainerCommand(runtime.ContainerSpec{
		Runtime:  rt,
		Image:    image,
		Name:     runtime.ContainerName(sess.Key(), strconv.FormatInt(m.now().UnixNano(), 36)),
		WorkDir:  sess.WorkDir,
		HomeDir:  sess.HarnessHome,
		EnvNames: names,
		Command:  plan.command,
	})
	if err != nil {
		return fmt.Errorf("container launch: %w", err)
	}
	plan.command = cmd
	return nil
}
