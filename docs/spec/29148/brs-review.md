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

## Open questions (Q-001 to Q-010)

Answer or reclassify here.

- **Q-001** Operator's answer, 2026-10-05: "marvel is an open source project without a specific intent around revenue, I'd like to see that it provides stable services for it's author for the next two months, and that within four months other users have chosen to use it to run some of their agent systems. I'd like to see it deployed on cloud computers for long term team agents. I'd like to improve new user experience and introduce majordomo within six months, leveraging AI for cluster internal operations." Recorded in brs.md as RULED claims C-039 to C-042 and C-044.
- **Q-002** Operator's answer, 2026-10-05: "marvel orchestrates and organizes agents on a scale beyond what an individual can manage, but enables people to advance with the tooling used by heroic solo developers because it works with rather than against the industry momentum in current REPL/harness tooling" Recorded in brs.md as RULED claim C-043.
- **Q-003** Operator's answer, 2026-10-05: "individuals operating major harnesses individually today, and individuals who would like to work better at scale and teams of individuals who would like to learn to work togeather to achieve more with their AI than they're able to operate without turning relying on SaaS for this function" Recorded in brs.md as RULED claim C-046. Partly answered: the groups are named; influence and key interests are not.
- **Q-004** Operator's answer, 2026-10-05: "Open source, but Inference service providers ToS in the use of their harnesses and services are important non-regulatory restrictions to be aware of" Recorded in brs.md as RULED claim C-045.
- **Q-005** Operator's answer, 2026-10-05: "that is the purpose of the reversing process to develop, git, git log, issues, design documents, kos nodes all for the basis for reversing the requirements, the CLAUDE and SOUL are also important, as are the .claude/rules" Answered: the reversing process is the route to the requirements, from the sources the operator lists.
- **Q-006** Operator's answer, 2026-10-05: "no; bd/dolt is not a native feature of marvel, but it is a service scheduled to become a marvel managed service option" Recorded as the operator said it: bd/dolt is not a native feature of marvel, and it is a service scheduled to become a marvel-managed service option.
- **Q-007** Operator's answer, 2026-10-05: "it's not the only finding conflict, sorry about that, workign towards a fix/renumber in time" Stays open; a renumber is in progress.
- **Q-008** (from C-020, open) The three Gateway sub-types in C-020 are services. Is "Gateway sub-type" the right classification for each of them, and what other services belong in the set? Operator's answer, 2026-10-05: "probably not the right classification. I don't remember the services involved but I felt that gateway was not a good match for those and that there were more services I'd described, vault (credential/secrets manager), bd/dolt (tasks), nats (agent bus/communications), litellm (router), PAIR (backend) and there were more" The classification stays open. Candidate services: vault, bd/dolt, nats, litellm, PAIR, and more.
- **Q-009** (from C-018, open) The widened ruling conflicts with finding-063 line 24 and the node's reopener, which keep the question open for Linux or a container. See Q-009 in `brs.md`.
- **Q-010** (new, open) `marvel daemon reexec` under mise restarts the old version (docs/admin-guide.md:666-680 at main 9343a8f, "Under mise: stop and start, not reexec"; marvel#523). Operator, 2026-10-05: "track it, we need reexec to work for mise somehow, even if we need to change the architecture". Tracked in marvel#592.
