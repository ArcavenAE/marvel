# vsdd-factory as a marvel team pack, measured side by side by critic

**Status: idea. Pre-hypothesis. Nothing here is designed or measured.**

Captured: 2026-10-04
Source: operator, relayed by director.

## The operator's words

> "kos idea wardrobe and sideshow we could create a wardrobe/marvel
> team+workflows/lifecycles/other primitives version of vsdd-factory for use
> on marvel, an adaptor that converts any vsdd-factory into a pack that is
> suitable to run in marvel and works similar to how that version of
> vsdd-factory works, but with true parallel agents rather than the agent
> switching and fanout that happens and we could run a test load of
> vsdd-factory in one agent session, and run the marvel/sideshow/wardrobe
> version and have critic measure them side by side on a test project/test
> spec, see how they perform for cost, cache amortization, throughput, speed
> and other factors"

## Where this lives, and why

The idea has two parts. One is an adapter that turns any vsdd-factory
release into a pack marvel can run: wardrobe definitions for its roles,
team manifests, workflows and lifecycles, delivered by sideshow. The other
is a side-by-side run that critic measures.

It is filed in marvel's graph by the subject test. If marvel did not exist,
there would be no arm B: the idea is a factory run as a marvel team, with
true parallel seats, and the comparison is of that run against the single
session. wardrobe, sideshow and critic are objects it uses: wardrobe supplies
the role definitions, sideshow delivers the pack, critic measures. This is
the shape of "pack management in marvel", which belongs to marvel however
many components it names. Director's first guess was wardrobe, and this was
first drafted at the orc (aae-orc#481); review pointed at marvel, and
working the subject test agrees. sideshow and critic have their own graphs,
and wardrobe has none yet; each gets a pointer below.

## The two arms

- **Arm A, vsdd-factory as it ships:** a test load run in one agent session,
  where the factory switches between agents and fans out from that session.
- **Arm B, the adapted pack:** the same factory converted to a marvel team,
  with each role a seat running in true parallel.

Both arms run on the same test project and test spec.

## What critic measures

The operator's axes, as given: **cost, cache amortization, throughput,
speed, and other factors.**

## Prior art this builds on

All of these are in the aae-orc graph.

- **finding-093, coexistence over supersession.** sideshow is a
  mechanism-plural installer, so a factory installed as a plugin and as a
  sideshow pack can coexist.
- **finding-094, unshaping over plugin delivery.** The ratified direction is
  to take plugin-packaged work, remove the limiting shape, and deliver it as a
  sideshow pack, and it names a future multi-agent conversion. This idea is
  that conversion, stated concretely.
- **ideas/post-plugin-pack-delivery.md.** What sideshow installs when the
  factory outgrows the plugin envelope (vsdd-factory#410 and its children).
- **ideas/marvel-agentic-resource-matrix.md.** "marvel is the substrate that
  vsdd-factory #410's `replatform` disposition asks for." Arm B is a
  replatformed run.
- **finding-168, wardrobe roles for dark-factory operations.** The role
  library a converted factory would be cast from. That subject (the role
  library itself) is the composition and sits at the orc; this idea's
  subject is the marvel run.
- **finding-138, the drbothen comparative analysis.** The earlier landscape
  comparison between the factory ecosystem and this platform.
- **question-agent-arena-evaluation.** critic's arena: judges, holdout, and
  how runs are compared.
- **question-model-selection-optimization.** Cost per accepted deliverable,
  and per-role model and effort choices, which arm B exposes and arm A does
  not.
- **question-marvel-beyond-mvp.** Packs and the organizational model among
  marvel's next steps.

## Pointers

- **wardrobe** (no graph yet): the role, team and lifecycle definitions an
  adapter would emit.
- **sideshow** (its own graph): the pack format and the install mechanism
  for the converted factory.
- **critic** (its own graph): the side-by-side measurement on a test
  project and spec, and the arena it would run in.
