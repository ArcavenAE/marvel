# Host operator (SRE), round 5: the vote

| # | vote | reason |
|---|---|---|
| V1 | A blocker | a stable release should not rewrite a binary at a path it did not install; if I lose, I dissent in one line below |
| V2 | A refuse mise only | Skeptic's check holds: any path outside brew and mise is `direct` (`internal/upgrade/store.go:110-119`) and README offers that download, so refusing it refuses the documented install |
| V3 | A close in C4a | the chair read the help at `cmd/marvel/main.go:519-536`; it already names the mise case |
| V4 | B should | a loud refusal with the key printable by `describe`, not a false reading |
| V5 | B should, if C14 rules (a) | every reader of the variable sits behind the fence under (a); it lands before the rc if ready |
| V6 | A should with a sequencing note and no edge | the meaning I wanted for "rc-window"; three seats show a named class invites accretion, so I take the plain form |
| V7 | C both | the snapshot job is the test that fails first on a PR; the tag-time check is the acceptance on the real archive |
| V8 | a single-host core only | a stranger's install runs with no bus (`internal/daemon/daemon.go:3149-3160`) |
| V9 | B stable gets its own name from the start | it is what README line 48 already says, and it moves no host on `brew upgrade` |
| V10 | A accept | it carries my C13 split and the C4 split |

**Predictable?**
- V8: no. ADR-005 and SOUL section 2 support the core being inside the promise, but no recorded ruling says whether the managed bus is also inside it.
- V9: no, with a pointer. README line 48 states the plan, but git cannot show that it is the operator's own word (Skeptic, round 4), so it needs a card or director's word that it counts as ruled.

**Taken from round 4 into my items.** Skeptic's test 3 is right: my C8 child test writes the read-only tree itself, so it stays red after the fix. C8's fail-first test becomes "the child runs `go env GOMODCACHE` under `RunWith` and asserts it is outside the scratch HOME". The removal-failure test stays. C9 keeps the per-tag check, not a count over the shared `/tmp`.

**Dissent, if V1 loses:** Host operator holds C2 a blocker: `marvel upgrade` changing a file marvel did not install is the host-side harm this party set out to remove.
