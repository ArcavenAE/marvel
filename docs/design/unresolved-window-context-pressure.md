# Context pressure for seats with no resolved window (codex, opencode)

Status: design, voted by a 3-round party (ptdd), 2026-10-06. Proposal only:
the rulings in section 6 are the operator's. Code references are pinned to
marvel `409593c`.

## 1. Why

marvel's context-pressure shift arm replaces a seat before its context runs
out. It fires when `ContextTokens > ContextLimit - HeadroomTokens`. The arm
skips a seat whose window is unresolved (`ContextLimit <= 0`) with no event or
advisory of its own (`internal/team/controller.go:2043-2055`). The usage
accountant does emit `context.limit-unresolved` once per session when it
measures tokens with no window (`internal/usage/accountant.go:288-292`), but
only for stream-fed seats, and the event says nothing about an arm that will
never fire. Today that
is every opencode seat, in both modes. Codex is better off than its ticket
assumed, but a fixed headroom and stale readings break it in other ways.

## 2. What is true today

- **Codex, interactive.** Seeded hooks run `marvel codex-ctx`, which reads
  the rollout and reports tokens and `model_context_window` by heartbeat,
  graded `feed` (`cmd/marvel/codexctx.go`, `internal/runtime/codex_home.go`).
  Measured on live seats: window 258400, which is 272000 x 0.95, an
  effective window and not a compaction point. One reading was six days old
  and still rendered as 15%. The model table carries no codex entry, on
  purpose (`internal/usage/limits.go:242-283`).
- **Codex, headless.** Whether the hooks fire under `exec` is not checked.
- **opencode.** There is no window in either mode unless the manifest
  declares `context_window`. Headless parses per-request occupancy;
  interactive has capture-pane only. marvel has no opencode hook, feed or
  database reader. opencode seats share the operator's opencode data
  directory (`internal/runtime/opencode.go:53,71` pass the base env only).
  That directory holds `opencode.db` and no separate auth file (names only,
  nothing opened), which is consistent with credentials living in the same
  sqlite file as the token counts.
- **The fixed headroom.** The usual headroom is 400000 tokens. Against a
  258400 window it fires on every tick (marvel#660). marvel#663 refuses a
  headroom at or above a *declared* window, but leaves windows resolved at
  runtime (feed, table) unchecked, and its 10-minute freshness bound would
  keep idle hook-fed seats from ever arming.
- **Compactions are counted** (`ContextCompactions`,
  `internal/usage/accountant.go:876`), and nothing under `internal/team`
  reads the count. The controller's stated aim is to shift before the harness
  compacts, because a post-compaction signal "would arrive one compaction too
  late" (`controller.go:1939`).

## 3. The credential boundary

The line is what marvel can reach, not which field it reads. A token count is
never custody. A marvel file handle on opencode's sqlite puts credential rows
one query away; that is on the wrong side of the SOUL section 3 custody
boundary and ADR-009's audience test. marvel receives only numbers opencode
chooses to emit, keeps only the extracted numbers, and never logs or exports
the raw payload (not to OTEL either).

## 4. Design (voted 5 of 5 on every item)

- **D1. One occupancy contract** for every adapter: level (never a running
  total), window, window source, as-of, compaction count or id, grade,
  observed by marvel or self-reported, harness version. The arm, the display
  and the advisories read only this. Codex already writes a
  compaction-generation id (`window_id`, `internal/runtime/codex/mapping.md:199`).
- **D2. Headroom as a fraction of the ceiling.** `headroom_fraction` applies
  to the ceiling: the lower of the window and a known compaction point, or
  the window when none is known. A window the adapter already discounted is
  not discounted again. `headroom_tokens` stays valid; at or above the
  ceiling it is refused at apply (declared window) or at arm time (runtime
  window), with an event. `get sessions` shows the headroom in tokens in
  force. Neither field is a default.
- **D3. Freshness by compaction.** A reading arms while no compaction or
  restart has happened since it was taken, because occupancy only rises
  between compactions. A seat that cannot report compactions is graded
  compaction-blind and arms only within the per-grade age bound of D4. A turn
  with no reading leaves the last one as a lower bound: it can miss a fire,
  never cause one.
- **D4. Stale display.** A reading older than a per-grade bound renders as
  stale, never as a live CTX%, whatever the arm does.
- **D5. ARM state.** A `get sessions` column: armed, no-window, stale,
  refused; qualifiers compaction-unknown and self-reported; a once-per-change
  event. It sits beside `context.limit-unresolved`, which stays as it is.
- **D6. max-age is required** beside any context-pressure arm, as the
  backstop for a faked or missing reading (a seat, or a prompt injected into
  it, can report any number). Apply refuses a context-pressure arm with no
  max-age arm.
- **D7. Compaction is a miss, not a trigger.** A compaction on an armed seat
  that was not shifted emits `arm-late` and counts as a miss per role; a
  compaction on an unarmed seat emits `compacted-unarmed`. There is no
  compaction-count trigger.
- **D8. No database read, enforced.** No marvel code opens opencode's
  database, as a probe or a build. A structural test fails if a marvel
  package imports a sqlite driver, runs `sqlite3` or `opencode db`, or opens a
  path in opencode's data directory. `go.mod` carries no sqlite driver today,
  so it passes on day one. It is a structural check, so it may gate (ADR-007).
- **D9. opencode, in order.** The window comes from the manifest. Then a
  private opencode home per seat, only once a probe shows how it
  authenticates without marvel touching credential rows. Then occupancy from
  a plugin, only once a probe shows a plugin can see token counts. The plugin
  is a file marvel vendors and writes into the seat's private home with its
  config entry, never installed with `opencode plugin` (which resolves npm
  and edits config), and it sends only D1 numbers. Until both probes pass,
  apply refuses a context-pressure arm on an opencode role, with an advisory
  naming max-age.
- **D10. No `serve` channel.** marvel does not launch or use `opencode serve`
  for occupancy. If headless `opencode run` binds a listener, marvel sets a
  per-seat password (issuance under ADR-009). A password in the pane env is
  readable by a sibling seat running as the same user, the known gap tracked
  in aae-orc-ww33y.
- **Compaction stays the harness's.** marvel's shift is a planned
  replacement ahead of native compaction, never a switch that turns it off.

## 5. Plan: flat tickets, red tests, edges

Builds:

| id | change | red test | after |
|---|---|---|---|
| T1 | occupancy contract (D1); fix the stale OpenCode note at `internal/runtime/events/events.go:155-160` | an adapter conformance test rejects a running total and a missing window source | |
| T2 | ARM state column and event (D5) | a live seat with a context-pressure arm and no window shows `no-window`; the event fires once per change | T1 |
| T3 | headroom fraction and runtime refusal (D2) | marvel#660's repro (window 258400, headroom_tokens 400000) is refused at arm time with an event; fraction 0.40 on 258400 arms above 155040 | T1, #663 |
| T4 | freshness by compaction (D3); replaces #663's 10-minute bound for feed seats | a live current-generation seat with a six-day-old reading and no compaction since arms; a reading from before a compaction does not; a compaction-blind seat arms only within the bound | T1, #663 |
| T5 | stale display (D4) | a reading older than its grade's bound renders `stale`, not a percentage | T1 |
| T6 | max-age required (D6) | a manifest with a context-pressure arm and no max-age arm is refused at apply | |
| T7 | `arm-late` and `compacted-unarmed` events (D7), from the same emitter as T2 | a compaction on an armed unshifted seat emits `arm-late`; on an unarmed seat, `compacted-unarmed` | T1, T2 |
| T8 | structural test against database access (D8) | a fixture package that imports a sqlite driver, or shells out to `sqlite3` or `opencode db`, fails the test | |
| T11 | refuse an opencode context-pressure arm at apply until T9 and T10 land (D9) | an opencode role with a context-pressure arm is refused at apply (today it is accepted and never fires) | |
| T9 | opencode private home (D9), checking the opencode version P3 recorded at spawn | a seat's opencode state lands in its own home; the operator's database is untouched | P3, ruling 3 |
| T10 | opencode plugin channel (D9) | a vendored plugin's readings arrive as D1 occupancy with grade and source | T1, T9, P4 |

Probes (each records the harness version it ran against):

| id | question | notes |
|---|---|---|
| P1 | codex's compaction point, and whether marvel can declare it in the codex config it writes | until it reports, every ceiling is the raw window; re-check headroom fractions then |
| P2 | do codex hooks fire under `exec` (headless)? | run early: if yes, codex occupancy is covered in both modes |
| P3 | an opencode private home: which variable moves only opencode's state, and how a fresh home authenticates | no database read |
| P4 | does a vendored plugin see per-request token counts? | read the plugin source first; any live run is the operator's, with an env-supplied key, and never links or copies the operator's database |
| P5 | does headless `opencode run` bind a listener, and with what auth? | the operator runs it |

T6, T8 and T11 have no dependencies and can ship first.

## 6. Rulings needed (the operator's)

1. Adopt the design and plan as voted. Recommended: adopt.
2. D6: require max-age beside a context-pressure arm (refuse at apply), or
   advise only. Recommended: require. All 19 roles in the one staged
   manifest change already carry a max-age arm, so nothing in flight breaks.
3. T9's authentication rule for an opencode private home, as the security
   seat stated it: authenticate from an operator-supplied env credential, or
   a link to a file holding only credentials; copying credential rows is
   custody; linking the whole database is the shared home under another
   name. Recommended: adopt.

Each recommendation is valid until 2026-10-20, or a change to the
context-pressure arm or an opencode or codex release that changes the facts
in section 2, whichever comes first; the architect seat that wrote this
re-checks it then. Nothing here executes on silence.

## 7. Record

The party record (casting, three rounds of replies, the checks run on
panelist claims, the ballot) is kept outside this repository, with seat
labels only. Panel: runtime substrate, AI security, context and memory
engineering, agent observability, OpenAI platform and Codex.
