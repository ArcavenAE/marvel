// Package admission is the arithmetic that refuses a spawn which would
// exceed a team-declared budget. It is the first brick of enforcement
// locus 2 (runtime admission and metering) in the agentic resource matrix;
// see marvel/_kos/nodes/bedrock/elem-agentic-resource-matrix.yaml and
// aae-orc-qiay.
//
// # Meter and gate are separate, on purpose
//
// internal/usage is a METER: it defines no threshold, no policy, and no
// refusal, because a metered value becomes a gate only through a ratified
// written decision (ADR-007 clause 3, SOUL.md section 8). This package is
// the gate, and the thing that ratifies it is the operator's own
// declaration in the manifest. No budget declared means no gate: an
// undeclared team behaves exactly as it did before this package existed,
// with no store read, no meter read, and no event.
//
// The package is pure. It imports internal/api and the standard library,
// and nothing else: no store, no accountant, no clock, no I/O. The caller
// gathers a Snapshot and hands it in, which is why the arithmetic is
// testable with neither a tmux server nor an Accountant.
//
// # The five rulings the design rests on
//
// R1. Repair safety is structural, not a flag. The manifest parser
// enforces sum(role.Replicas) <= max_sessions, so declared <= budget is
// an invariant of every parsed manifest. Converging a role toward its
// declared replicas therefore cannot cross the team cap, so no "is this
// growth?" predicate exists anywhere in the code and repair can never be
// refused. The invariant is only as strong as the doors that hold it:
// `marvel scale` edits a replica count without re-parsing a manifest, so
// it carries the same clause at the verb (daemon.admitDeclaration).
// Without that, scaling while a replica was dead committed a declaration
// the parser refuses. Where the invariant is violated anyway (an
// out-of-band UpdateTeam), papering over it with more spawns would be
// wrong, so the clause refuses even repair and names the two numbers that
// disagree.
//
// R2. A cumulative clause is never evaluated on the repair path.
// usage.TeamSpend is monotonic within a daemon lifetime: retired spend is
// rolled into the team total at Forget and nothing subtracts. Gating
// repair on a monotonic meter is a permanent outage, not a budget. Growth
// carries the cumulative clause; repair does not.
//
// R3. Partiality can only understate, so refusal on a partial total is
// sound. Every source of partiality contributes zero: an unobserved
// session, a nil Reader, a pane adopted from a prior daemon. The measured
// total is therefore a floor, and spent >= limit implies true >= limit.
// Admission on a partial total is the ambiguous direction, which is where
// marvel speaks (Indeterminate plus an event, resolved by
// api.Budget.OnUnmeasured).
//
// R4. A refusal must never touch crash bookkeeping. Both of the team
// controller's crash paths funnel into noteCrashAndBackoff, whose
// MaxRestarts saturation freezes BackoffUntil in the year 9999 and writes
// that through to bolt. A budget refusal routed there would become an
// unrecoverable role kill that survives a restart. A refusal is not a
// crash, and this package never learns about restart counts.
//
// R5. Shift overlap is exempt from count-shaped clauses by shape, not by
// special case. A shift is replacement; its transient double count is a
// mechanism artifact, so Request.Overlap skips ShapeCount clauses and does
// not skip ShapeCumulative ones (a new generation is a new spender). The
// operator-visible consequence is that live sessions can read up to twice
// a role's replicas for the length of a rotation, so the exemption has to
// be stated wherever the ceiling is described: api.Budget.MaxSessions,
// docs/user-guide.md, and the Row note while a shift is in progress.
//
// # Dimensions excluded on evidence
//
// Three registry rows are declared in internal/api and deliberately have
// no evaluator here:
//
//   - max_cost_usd. Claude lifts total_cost_usd only on its terminal
//     result line, so a running claude team reads CostUSD == 0 with
//     CostReported == false; codex publishes no cost at all. Only
//     opencode is live per request. A dollar ceiling is therefore
//     unenforceable mid-flight on any team containing claude or codex,
//     and making it work needs the per-harness capability table that
//     lives in internal/usage.
//   - max_session_ctx_percent. Context occupancy is a level and never a
//     sum (internal/usage/doc.go), so there is no team aggregate to
//     compare against a ceiling. How context pressure aggregates across a
//     team is an open question owned by the shift-trigger work
//     (_kos/nodes/frontier/question-shift-triggers.yaml); admission must
//     not answer it first by accident.
//   - max_team_rss_bytes. Process metrics are honest about their own
//     limits and cannot feed a gate: MetricsAt is zero for a just-spawned
//     session, the sampler runs at 5s against a 2s reconcile tick,
//     CPUPercent is a subtree rollup with no capacity denominator, IO
//     counters are unavailable on darwin, and metrics are deliberately not
//     persisted.
//
// Per-provider rate-limit accounting is not excluded pending work: it is
// unachievable. Marvel cannot see a provider, because auth delegates to
// the harness and marvel stores no credentials. The enforceable boundary
// is workspace/team.
//
// # Standing warning
//
// This package is the natural attractor for wiring a health metric into a
// gate. Someone will want a context-percent ceiling next, and it will look
// like a five-line addition. It is not: a vital sign becomes a gate only
// through a ratified written decision, and structural validity is the only
// thing that may gate without one. The behavior trigger for that moment is
// aae-orc's .claude/rules/diagnostic-not-gate.md.
package admission
