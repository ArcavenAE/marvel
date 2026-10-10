# Adapter kit checklist: what a new harness walks before it reaches the feature matrix

A harness gets a marvel adapter only after a builder can tick every line below, or record why a line does not apply. Each line is a check, not a goal: it names what has to be shown, and where. The lines came out of the goose adapter v1 design (`docs/design/goose-adapter-v1.md`). They were walked against a second harness, gemini-cli, so that they do not fit only goose. Proposed 2026-10-10; it becomes the kit once the operator rules on the design.

Lines that are not met yet still go into the adapter doc, with the reason. A `no` with its reason is a valid answer, and so is `UNCHECKED` with the probe that will settle it. A silent gap is not.

## Pin and sources

1. The pin is an exact release tag with its commit sha and date, never a moving channel or tag (`stable`, `latest`, `canary`). It has a re-check date and a re-checking role.
2. Levers are read from source at the pinned tag, not from docs alone. Where docs and source disagree, the adapter doc notes it.

## Exit status and denial

3. Every exit code the adapter maps is shown in source or observed, and each maps to a named reason. Where one code covers several reasons, the adapter doc names what tells them apart (stream line, stderr file, store row).
4. Exit status is measured for success, a denied tool, approval needed in headless, a provider error and the turn cap, and recorded in the matrix evidence.
5. Denial is recorded per call and per run: whether a denied call ends the run, and the last stream line and exit code it leaves. Exit 0 is checked against the stream before a run is called clean.

## Posture

6. The headless permission mode is set explicitly. Source shows whether a prompt in headless fails closed, parks, skips or auto-allows. Every auto-allow branch, including one raised by a non-permission check, and every silent posture rewrite (a condition under which the harness changes the requested mode) is listed with its source line and the modes it applies in.
7. The posture for an unattended role is fail-closed or sandboxed. The source line names which modes honour the deny list, and whether the list can constrain a shell tool's arguments.
8. A posture fault never goes through a `Prepare` error, because that launches the raw command (`internal/session/manager.go:995-999`). It goes through the bootstrap refusal path once K1 exists (`docs/design/seat-bootstrap.md` section 5b). Until then it goes through a refusal stub that never runs the harness and exits a marvel-reserved code.
9. The posture in force is read back after launch from a field the harness writes. Where no such field exists, the matrix says so.
10. Every adapter-set env key is tested for precedence against the role env merge. A posture key the deny list depends on is adapter-wins, and a conflicting role value is refused.
24. No degraded launch of a headless role uses a mode that skips or denies tools and still exits 0, because exit 0 reads as succeeded (`internal/session/manager.go:1553-1580`).
25. A seat with an unrestricted shell tool is not called contained because of its permission list. Containment is the sandbox, and the harness's own sandbox counts only if it covers the binary marvel runs.
29. The marvel-reserved refusal code is shown in source never to be an exit code the harness itself returns.

## Launch and stream

11. Headless stdin is redirected from `/dev/null` unless it is measured that the harness does not read it.
12. In structured mode stdout holds only machine lines, or the parser skips the others.
13. The parser has `mapping.md` naming the harness version and date, plus live fixtures. No lifecycle event is invented without a vendor line; a vendor terminal line may map to `session.ended`.
14. The terminal event and every event that carries usage are classified as level or cumulative, with a two-request fixture. Cumulatives stay out of the level fold.
26. The failure reason survives the reap. It is kept in a per-launch file in the session home, or read before the dead pane is reclaimed (`internal/session/manager.go:1533-1539`), or the matrix records it as lost. Kept text is bounded and scanned for credential shapes before storage.

## Context

15. The adapter names the producer that stamps `ContextAt`. A seat whose only signal is spend reads quiet (`internal/api/store.go:853-860`), and the matrix says so.
16. A harness-owned database or session file the adapter reads is opened read-only, with its schema version pinned. An unknown version is refused. When the store holds rows for several launches or sub-sessions, the row is selected by a bound known at launch (start time, session type, no parent), not by recency alone.
17. If the harness has a window env, it is projected from `runtime.context_window`.
23. The channel that carries the per-request level (stream, store, hook or OTEL) is shown in source at the tag. If there is none, the matrix cell is `no`.

## Identity and home

18. `SessionIDAssigner` only when the harness accepts a caller value at launch, stores it, and on resume selects by it uniquely, shown in source and in the harness's own store or output. It declines when the role's args already steer identity.
19. A private home: one absolute lever, built by marvel, moves config and sessions together. A run shows nothing written under the operator's home and that a shared store is no longer shared. Any state the lever does not move (an OS keyring entry, a fixed service name) is named with its source line. A relative or empty value is refused.
28. Files the harness auto-loads from the workdir and its ancestors (`.env`, project config, hint files) are listed, and a seat workdir is checked for them.

## Credentials and telemetry

20. No provider credential appears in launch env, args, seeded files or captured fixtures. Credentials are linked or brokered, never copied (ADR-009).
21. Harness telemetry export, content capture and vendor usage statistics are off by default at launch, each named in the projected env or a seeded file. Any telemetry file marvel keeps is checked for identity fields. Background model calls the harness makes on its own account are turned off where a lever exists.

## Registration

22. The harness is registered and the matrix regenerated with `MARVEL_UPDATE_MATRIX=1`, with every new cell cited.
27. The adapter doc lists each optional interface as yes or no, with a cite, matching its `init` assertions.

Line numbers are kept from the design vote so that votes and reviews can cite them; they are grouped here by subject, not by number.
