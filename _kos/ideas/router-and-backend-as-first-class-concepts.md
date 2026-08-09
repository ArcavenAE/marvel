# Router and backend: two concepts marvel is missing, and the model may not be knowable

**Status: idea. Pre-hypothesis. Nothing here is measured, and the central
question is deliberately left open.**

Raised by the operator 2026-08-08, out of the context-pressure arc. The
observation is that marvel's model of "what is serving this agent" has
exactly one rung (a harness, running a model), and reality has at least
three (a harness, talking to a ROUTER or directly to a BACKEND, which may
itself fan out to several backends).

## The two concepts

**Backend.** The thing that actually serves inference. lmstudio on the
local box, a cloud provider endpoint, a hosted API.

**Router.** Something sitting between the harness and one or more
backends, selecting per request. liteLLM is the concrete instance.

## The open question, stated as the operator posed it

When a router is NOT used, the backend is direct: the harness talks to
one place and marvel could in principle know which.

When a router IS used, it is less clear what marvel is even looking at.
Maybe the router IS the backend from marvel's point of view, and the real
backend behind it is not marvel's business. Or maybe the router is a
distinct layer that marvel should see THROUGH. **And we might not know
the model or the backend at all**, because the router chose it, possibly
per request, possibly by rules marvel never sees.

This is not resolved and should not be resolved by assertion. It wants
study.

## The concrete instance driving it

`local-ai-mac` runs three modes:

- **local**: backends only (lmstudio among them), no router.
- **cloud**: backends only, no router.
- **hybrid**: liteLLM as a router, fronting various backends.

So the same operator, the same fleet, has configurations where a router
exists and configurations where it does not. Any model marvel adopts has
to describe both without special-casing one.

## Why this lands on marvel rather than staying an operator concern

The operator's framing: marvel will need to know these concepts, PRESENT
them, and possibly manage, configure, or show what is happening with the
agents. Three distinct asks with different costs, worth keeping separate:

1. **Know**: carry router and backend in the model at all.
2. **Present/show**: surface which agent is being served by what, live.
3. **Manage/configure**: declare routing in a manifest, or reconcile it.

(1) is cheap and probably right. (3) is a large claim about ownership and
should not be assumed from (1).

## Why this is urgent rather than merely absent

The context-pressure arc just spent a session establishing that CTX%
needs a denominator, and that the denominator varies on six independent
axes, one of which is BACKEND SERVICE (finding-016). A router does not
add a seventh axis. It does something worse: **it can make the axis
unobservable.**

- The model-to-window table is keyed on a model marvel believes is in
  use. If a router silently served a different model, the table returns
  a confident wrong number, which is the exact silent-wrong failure this
  whole arc keeps ruling against.
- `internal/usage`'s discipline says an unresolved window renders absent
  rather than guessed. A router may be a new and common source of
  "unresolved", and marvel currently has no way to even name why.
- Rate limits (`reif`) bind per account at a backend. Behind a router
  they may bind at the ROUTER's pool instead, or at several backends
  independently, which changes what the headroom signal even means.
- Cache locality, one of the resource matrix rows with real dollar
  consequences, is a property of a specific backend's cache. A router
  that moves a session between backends destroys locality invisibly.

So the honest short version: **a router breaks the assumption that
marvel knows what it is metering.** That is worth naming before more
metering is built on the assumption.

## What a study would have to settle

- Is the router a distinct layer in marvel's model, or is it just
  "the backend" seen from outside? Both are defensible and they lead to
  different manifest surfaces.
- Can marvel LEARN the served model and backend per request from any
  harness, or does the router make this structurally unknowable for some
  configurations? If unknowable, what is the honest rendering? (The
  `?`-rather-than-guess discipline already has the answer shape.)
- Does liteLLM (and the router class generally) expose the resolved
  model and backend in a response header, a log, or an admin API? This
  is the same channel-catalog question the CTX% sweep just answered for
  harnesses, pointed one layer down.
- Where do rate limits and spend actually accrue behind a router, and
  can marvel attribute them?
- Which of know / present / manage is marvel's, and which belongs to the
  operator's own router configuration? SOUL's no-conscription rule
  argues against marvel owning a router it did not install.

## Relationship to what exists

- **finding-016** names "backend service" as one of six denominator
  axes. This idea says that axis has internal structure, and may be
  opaque.
- **`elem-runtime-names-harness`** is the ratified rule that `runtime`
  names the HARNESS, never the agent. Router and backend are a THIRD
  thing that is neither, which is some evidence they need their own
  names rather than being crammed into `runtime`.
- **`aae-orc-2lfg`** (per-agent model/provider selection in the team
  manifest) is the adjacent open ticket and probably the place a
  manifest surface would first appear.
- **`aae-orc-reif`** (rate-limit headroom channel) has a hard dependency
  on knowing where limits bind, which a router changes.
- **The bet memo** already predicts fleets coordinating local hardware
  with cloud frontier models, and names placement across the
  cost/privacy/latency/capability/cache-locality gradient as
  load-bearing. A router is the mechanism by which that placement
  happens, so this is the bet's machinery arriving early.
- **The agentic resource matrix**: cache locality and token spend are
  both rows whose meaning depends on which backend served the request.

## What would kill this idea

- If every harness marvel targets reports the resolved model and backend
  per response, there is no observability problem and this is just two
  fields.
- If operators running routers do not want marvel to see through them
  (a deliberate abstraction boundary), then "the router is the backend"
  is the right answer and the study is short.
- If routing turns out to be stable per session rather than per request,
  the problem shrinks to a one-time resolution at spawn.

## Provenance

Operator observation, 2026-08-08, during the context-pressure fan-out
planning. Grounded in a running configuration (`local-ai-mac`:
local / hybrid / cloud, liteLLM as the hybrid router, lmstudio among the
backends) rather than in speculation.

Related: `finding-016-effective-autocompact-window-is-the-predictive-denominator.md`,
`fleet-throughput-as-the-objective-function.md`,
`context-pressure-is-an-operating-point-not-a-fill-level.md`,
`elem-runtime-names-harness`, `elem-agentic-resource-matrix`.
