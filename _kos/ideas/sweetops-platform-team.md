# A SweetOps platform engineering team marvel can run, parameterized by an org's native protocol

- **Status:** idea (pre-hypothesis, no commitment). Operator request relayed
  via director, 2026-09-26.
- **Subject:** marvel (a team manifest marvel runs, and what marvel must
  provide for it). The role and identity content is an OBJECT marvel consumes;
  where it lives is an open question below. Filed in the marvel graph per the
  subject test.
- **Related:** [[shift-change-succession-protocol]] (a long-lived platform
  team is the case where succession matters most). wardrobe (B14 role library;
  the existing `author`, `reader`, `builder`, `reviewer` roles are the nearest
  shapes). aae-orc B14 (persona, identity, role, process as separate library
  items).

## The observation

A reusable platform engineering team that marvel can run, with deep expertise
in the SweetOps method and its tools: Atmos, Atmos Pro, the Cloud Posse
component library, `null-label` context, stack configuration, vendoring, and
the Terraform conventions around them.

The expertise is generic. What differs from one adopting org to the next is a
set of local variants, and those are what make a generic SweetOps agent wrong
in a specific repo. The idea is to split the two:

1. **The team** (reusable): roles and identities that know SweetOps as
   practiced upstream, and a process for the work such a team does (component
   bumps, new stacks, drift, reviews, onboarding a new account or region).
2. **The native protocol document** (one per adopting org): a structured
   statement of that org's variant for each aspect, which the team reads
   before acting. Examples of the aspects it covers:
   - repositories and repo types (which repo holds stacks, which holds
     components, which are consumers, which are forks);
   - `null-label` usage and stack definitions: the context and naming
     conventions (namespace, tenant, environment, stage, name order and
     delimiters) and the stack layout (catalog, mixins, orgs, imports);
   - a Terraform style guide (module structure, variable and output
     conventions, provider pinning, formatting and lint rules);
   - approval groups (who reviews what, which paths route where, what needs
     a human);
   - additional sources of Atmos components beyond upstream (internal
     component repos, forks, vendored pins, and how each is updated);
   - and similar variants as they surface: backend and state layout,
     account map, CI and plan/apply flow, secrets handling.

The same team, handed a different protocol document, should behave correctly
in a different org without its role text changing. A missing or silent aspect
in the protocol is itself a finding for the team to report, not a gap to fill
with the upstream default.

## Tensions

- **Where team and role content lives versus what marvel provides.** The
  roles could be wardrobe items (B14-decomposed, versioned, index-digested)
  or come from another role system. Marvel's part is the manifest, the
  spawn, the environment, and supervision. Which of the team's needs (for
  example per-repo worktrees, a plan-only credential, a review route) are
  marvel capabilities and which are role text is not yet drawn.
- **How the protocol document is loaded.** Three candidates, not chosen: a
  seat file placed at spawn; a pack (sideshow, versioned and signed, one per
  org); or a kos backdrop in the adopting org's own graph (authored, not
  summarized, read at orient). They differ in who owns updates, how drift is
  detected, and whether an agent can read the version it ran against.
- **Relation to existing tooling.** The ACU automation (the Atmos component
  updater bot that opens component-bump PRs, and the triage commands built
  around it) and Atmos Pro (plan and apply orchestration, drift detection)
  already do parts of this work. The team should drive and judge that
  tooling rather than duplicate it; where the boundary sits is open.
- **Generic expertise versus current upstream.** SweetOps conventions move.
  The team's upstream knowledge needs its own freshness story, separate from
  the org protocol's.
- **Authority.** Infrastructure changes have a wide blast radius. The team's
  write domain (PRs only, human merge, which approval groups) should come
  from the protocol document, not from the role, so each org sets its own.
- **One protocol or several.** An org with more than one platform repo shape
  may need one document per repo type, or one document with sections. Unclear
  until a second org is tried.
