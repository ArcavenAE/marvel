# CLI read-back and one-off parity: which verbs are missing, ranked

Design for review. No code lands until this doc is reviewed.

- Author: the marvel builder seat.
- Ticket: aae-orc-6kbs0, an exploration whose output is this note and a list of
  flat follow-on tickets.
- Checked against marvel `origin/main` e056128. The ticket was written on
  2026-09-19 against two alpha builds, so each premise is re-checked here.

The ticket asks four things: read the working state back (`get -o`,
`describe`), make `run` and `work` stamp the same environment, say what the
per-session backend work needs from the CLI, and decide what the bus verb
becomes. Section 1 checks the premises; section 2 ranks what is left.

## 1. The premises, checked

| Premise | Where | Result |
|---|---|---|
| `get` has no output format flag | `marvel get -h` lists `--columns`, `--header`, `--no-trunc`, `-w`; no `-o` | true |
| `describe` prints the stored record as JSON | `handleDescribe`, `internal/daemon/daemon.go:1539`; `handleGet` (`:1453`) marshals the store lists the same way | true |
| There is no way to get the manifest the daemon is running | no verb emits TOML or YAML; `Manifest.Apply` (`internal/api/manifest.go`) is one-way | true |
| `scale` edits the stored team, so a file can drift from it | `handleScale`, `daemon.go:1653`, updates the team record | true for scale; run and shift not re-read here |
| Read surfaces must not print declared env and args | `docs/design/describe-redaction.md` (#423): `Env`, `Args` and `Prompt` go out whole today | true: no redaction code exists in `internal/` or `cmd/` at this commit, so any `-o` has to go through the view that design describes, once it lands |
| A `run` one-off is stamped only `MARVEL_SESSION`, `MARVEL_ROLE`, `MARVEL_SOCKET` and the heartbeat token | `directCommand`, `internal/session/manager.go:1330` | true |
| Why: a one-off has no team record, so launch takes the direct path | `handleRun`, `daemon.go:1994`, creates the session without one; `manager.go:912-915` falls back to `directPlan` when `GetTeam` fails | true |
| A work role gets the full set | `constructedEnv`, `internal/runtime/adapter.go:389`: `MARVEL_TEAM`, `MARVEL_WORKSPACE`, `DIRECTOR_AGENT_ID`, `DIRECTOR_TEAM`, `DIRECTOR_WORKSPACE`, `BEADS_ACTOR`, plus the bus URL and credentials when the cluster has a bus | true |
| Extra args after `sh -c '<script>'` crash the spawn | `directCommand` joins args with `cmd += " " + arg` and no quoting; the adapter path quotes each arg itself (comment in `internal/team/headless_completion_test.go`) | mechanism found by reading; not reproduced, because a one-off cannot be spawned on the live fleet from this seat |
| A one-off under an existing team is reconciled as a role and its death emits `role.removed` | `role.removed` is emitted for a role dropped from a re-applied manifest (`internal/team/controller.go:902`); the path for a one-off was not traced | not checked |
| `marvel bus` has only status | `marvel bus -h`: `status` and `leaf`; daemon methods `bus.status`, `bus.leaf.connect`, `bus.leaf.disconnect` (`daemon.go:1048-1056`) | true |
| A backend verification hook is missing | `marvel backend verify` exists (`cmd/marvel/backend.go`) | false: part of the ask has landed |
| A per-role env overlay is missing | `Runtime.Env` is declared in the manifest (`manifest.go:233`) and copied at apply (`:831`) | false: the overlay exists; what is missing is a `get`/`describe` view that shows it safely |
| Output tokens are not shown | the `TOUT` column is in the wide set (#680) | false for output tokens; input tokens not checked |
| Session handle (tty, pane, cwd) and shim revision for presence | not investigated | open; the question belongs to the director side |

## 2. What is left, ranked

Ranked by cost against what it unblocks. Each item is one flat ticket.

1. **Stamp identity by construction on the direct path.** A one-off cannot
   join the bus because it lacks `MARVEL_TEAM`, `MARVEL_WORKSPACE` and the
   `DIRECTOR_*` identity set, all of which `run` already knows (`--team`,
   `--workspace`, `--role`, the session name). The fix is mechanical: one
   function that returns the identity and placement variables for both
   paths, so they cannot drift apart again. The issuance variables (bus user
   and password) are a separate decision, below.
2. **Quote the direct path's args.** Same function, same ticket family: the
   direct path should quote each argument the way the adapter path does. The
   first step is a repro that writes `$#` and each argument to a file.
3. **`get -o json|yaml` for teams and workspaces, through the redacted
   view.** Re-applyable output is the part the ticket wants most, and the
   hard part is not the encoder. The store holds the desired team after
   `scale` and similar edits, so the output is the desired state now, not
   the file that was applied. Say that in the verb's help, and do not promise
   a round trip to the original file (comments, ordering and unset fields are
   not kept).
4. **`describe` as a human view.** A summary (state, generation, health,
   recent events, per-role runtime summary) in place of raw JSON, with the
   JSON staying behind `-o json`. Depends on item 3 for the shared view.
5. **Backend and env on `describe`.** The overlay exists; show which
   variables a role overrides by name, never by value, and keep
   `marvel backend verify` as the check of what a session is actually on.
6. **Bus roster and inbox depth.** New daemon methods behind `marvel bus`
   (roster with instance and age, inbox depth per session, undrained retired
   inboxes, durable counts). Keeping this under `bus` and not under
   `get sessions -o wide` keeps one meaning per verb; the name survives the
   two-tier model because `marvel bus` is the local cluster's broker and the
   global hub is the director's.

## 3. Decisions this note needs from a person

- **Issuance for one-offs.** Should a `run` session get a bus credential at
  all? A one-off has no team user, so the options are the cluster seat user,
  a short-lived per-session credential minted by the daemon (brokering is
  permitted under ADR-009), or none, with the one-off documented as off the
  bus. Recommendation: none until a use case names one, because the identity
  variables in item 1 already let a one-off that is handed credentials by
  hand join the bus.
- **What `get -o` reflects.** Recommendation: desired state as stored, labelled
  as such. A diff against the last applied file needs marvel to keep that
  file, which it does not today.
- **Whether items 1 and 2 are one ticket.** Recommendation: two, since the
  second has a repro to write first.

This recommendation is valid until 2026-11-08 or until the redaction view
lands, whichever comes first; the marvel builder re-checks it then.

## 4. Follow-on tickets proposed

Not filed: filing is the ticket owner's call, and none is committed to a date.

1. direct path stamps identity and placement from one shared function
2. direct path quotes each arg, with a `$#` repro first
3. `get team|workspace -o json|yaml` through the redacted view
4. `describe` human summary
5. `describe` names the env a role overrides, never the values
6. `marvel bus roster` and inbox depth
