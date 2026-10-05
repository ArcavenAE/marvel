# BRS review

The operator reviews `brs.md` and fills this file in. The verdicts on rows C-013 to C-020 were relayed by director on 2026-10-05, quoted verbatim in the Notes; check a row against that relay to repeat the fidelity check. One row per claim, with a verdict:

- `accept`: the claim stands as written.
- `accept-reworded`: the claim stands and only the wording changed. Listed, not counted as a correction.
- `correct`: the claim's class, sources or content changed.
- `reject`: the claim is removed.
- `add`: a claim the draft missed. Add these under "Added claims" below.

The correction count is `correct` plus `reject` plus `add`. It is computed after this file is filled in, and not before.

| Claim | Class | Verdict | Note |
|-------|-------|---------|------|
| C-001 | OBSERVED | | |
| C-002 | OBSERVED | | |
| C-003 | OBSERVED | | |
| C-004 | RULED | | |
| C-005 | OBSERVED | | |
| C-006 | OBSERVED | | |
| C-007 | RULED | | |
| C-008 | RULED | | |
| C-009 | RULED | | |
| C-010 | OBSERVED | | |
| C-011 | OBSERVED | | |
| C-012 | OBSERVED | | |
| C-013 | RULED | accept | Operator: "all the rest that were displayed are good/approved". |
| C-014 | OBSERVED | accept | Operator: "all the rest that were displayed are good/approved". |
| C-015 | OBSERVED | accept | Operator: "all the rest that were displayed are good/approved". |
| C-016 | OBSERVED | accept | Operator: "all the rest that were displayed are good/approved". |
| C-017 | RULED | accept | Operator: "all the rest that were displayed are good/approved". Recorded as written. |
| C-018 | RULED | correct | Operator: "c-018 the macos specifics are an artifact of rapid development and not having a linux system in active use at development time, this should not be limited to macos". Claim reworded in `brs.md` to be platform-neutral; the macOS measurement is kept as the evidence, not as the scope. |
| C-019 | OBSERVED | accept | Operator: "all the rest that were displayed are good/approved". |
| C-020 | OBSERVED | correct | Operator: "c-020 these are \"services\" and it's not clear this is the correct classification for each and they are not limited these". The three are services; whether each is correctly classified is open, and the set is not limited to these three. See Q-008. No new classification is made here. |
| C-021 | FORWARD | accept-reworded | Operator: "15 C-021 raccept reworded to include that marvel daemon reexec performs an upgrade while retaining running processes / accept the rest". Claim text now says `marvel daemon reexec` performs an upgrade while keeping running processes. |
| C-022 | OBSERVED | accept | Operator: "accept the rest" (relayed with the C-021 verdict). |
| C-023 | FORWARD | accept | Operator: "accept the rest" (relayed with the C-021 verdict). |
| C-024 | RULED | accept | Operator: "accept the rest" (relayed with the C-021 verdict). |
| C-025 | OBSERVED | accept | Operator: "accept the rest" (relayed with the C-021 verdict). |
| C-026 | OBSERVED | accept | Operator: "accept the rest" (relayed with the C-021 verdict). |
| C-027 | JUDGMENT | accept | Operator: "accept the rest" (relayed with the C-021 verdict). |
| C-028 | JUDGMENT | accept | Operator: "accept the rest" (relayed with the C-021 verdict). |
| C-029 | JUDGMENT | accept | Operator: "All the reset are good, reword and accept those above". |
| C-030 | JUDGMENT | accept | Operator: "All the reset are good, reword and accept those above". |
| C-031 | OBSERVED | accept-reworded | Operator: "C-031 reword, this is an artifact of rapid development, macos AND linux are the target platforms we haven't determined which keychain system we'll require and support in linux, but it will NOT require GUI/Gnome/KDE/etc x or wayland". Claim text now covers macOS and Linux, says the Linux keychain is not yet chosen, and rules out a GUI desktop or display server. |
| C-032 | OBSERVED | accept | Operator: "All the reset are good, reword and accept those above". Accepted as written. |
| C-033 | OBSERVED | accept | Operator: "All the reset are good, reword and accept those above". |
| C-034 | OBSERVED | accept | Operator: "All the reset are good, reword and accept those above". |
| C-035 | OBSERVED | accept | Operator: "All the reset are good, reword and accept those above". |
| C-036 | OBSERVED | accept-reworded | Operator: "C-036 but we've shifted from BYOA to BYOH (harness) although BYOA is still valid (bring your own harness, bring your own agent definitions or get them with sideshow or wardrobe, or bmad or vsdd-factory or gastown (future))". Claim text now frames BYOH and keeps BYOA valid. |
| C-037 | OBSERVED | accept | Operator: "All the reset are good, reword and accept those above". |
| C-038 | OBSERVED | accept | Operator: "All the reset are good, reword and accept those above". |

## Added claims

(none recorded yet)

## Open questions (Q-001 to Q-009)

Answer or reclassify here.

- **Q-008** (from C-020, open) The three Gateway sub-types in C-020 are services. Is "Gateway sub-type" the right classification for each of them, and what other services belong in the set? The operator says the set is not limited to these three and the classification of each is not clear.
- **Q-009** (from C-018, open) The widened ruling conflicts with finding-063 line 24 and the node's reopener, which keep the question open for Linux or a container. See Q-009 in `brs.md`.
