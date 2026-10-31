# Seat lifecycle, round 4

Read-only on fa228925. Each row gives my final class and the strongest case against it.

## 1. Contested rows

| id | final | strongest argument against my choice |
|---|---|---|
| C2 | should | a stable user pins more often than an alpha user, and a pinned binary changed under them is a host change nobody asked for |
| C6 | should | it is the second command a stranger types, and the fix (resolve one unambiguous name) is small enough that holding it out saves nothing |
| C7a | should, landing before C1b | `MARVEL_PREDECESSOR` is the only succession link marvel gives a seat, used for more than mail (a successor's handoff read and its claim on its predecessor's worktree lock), so a crash replacement that lacks it is a core inconsistency: shift sets it (`internal/team/controller.go:2472`), crash repair does not |
| C8 | should, landing before C1b | an order note without a gate tends to be skipped, which argues for a class that holds the tag |
| C15 | should | Ubuntu 24.04 ships tmux 3.4, so the commonest Linux first run pays a headless run twice with no word why |
| C14-scope | (a) single-host core, with the bus and fleet verbs experimental | the fleet runs on the bus every day, so "stable" would describe the part this fleet uses least |
| C1-name | alpha keeps `marvel` for the rc; ruled before `v0.1.0` | two moves of the plain name (one now, one at `v0.1.0`) cost more confusion than one move now |

On C7a: I said in round 3 it drops to should if the bus is outside the promise. Under C14 (a) every consumer of the variable (director-mcp, the handoff read, the worktree owner check) is a fleet feature, so I keep my word. It has no edges and is small, so it should land before C1b.

On C8: "should, before C1b" is not a class of its own. It is a should with an order edge, and the outcome should record it as an edge ("lands before C1b; does not block it"). A fourth class invites every should to argue for it.

## 2. Product scope: can the ruling be predicted?

**C14-scope: partly.** SOUL section 2 and ADR-005 make (c) unlikely: marvel must work with no bus, so a promise that covers the bus promises what a stranger never runs. Nothing recorded picks between (a) and (b). The card must contain:
- the stable verb list. Use First adopter's round-3 list, not the brief's eight: (a) as written leaves out `delete`, `scale`, `stop`, `daemon`, `reap`, `version`, `config` and `keys`, and a stable install cannot run without `daemon` or clean up without `reap`;
- what "experimental" means in practice: no compatibility promise, a help banner, a NOTE at apply;
- what each option does to the classes here: under (b), C7a, #953 (C13) and C7b move to blocker; under (a) they stay should;
- the cost of each option: (a) the label covers the smaller surface; (b) the bus's hub and leaf code joins the promise, with #883, #855 and #514 open against it; (c) everything open joins the promise.

**C1-name: no.** It decides what every host on the tap gets on its next `brew upgrade`, and no recorded ruling covers channel naming. The card must contain:
- the silent downgrade under option A (an alpha host moves to an older rc), which Release engineer measured from `internal/upgrade/upgrade.go:121`;
- the name stable would take under B;
- whether a one-time note can ship with A;
- an expiry tied to the `v0.1.0` cut, not the rc.

## 3. (Skeptic only)

Not mine.

## 4. One edge

Missing rather than wrong: **C13 blocks C1b.** The known reading gaps section is part of the rc's notes, and the #1131 advisory is in the rc by the round-3 tally, so the tag cannot be cut before both. The chair's order has C4 blocking C13 but no edge from C13 to the tag, which would let the rc ship notes without the gaps list. Check: round-3 brief, C13 row ("#1131 into the rc").
