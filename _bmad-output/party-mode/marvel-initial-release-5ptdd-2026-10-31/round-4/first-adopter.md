# First adopter, round 4

Lens unchanged: a stranger with the installed binary, tmux and one harness.

## 1. Contested rows

| id | my final | strongest argument against it |
|---|---|---|
| C2 #756 | should | a stranger who pins with mise and runs `marvel upgrade` loses the pin with no word, and that is a change to their host nobody asked for |
| C6 #337 | should, moved from blocker | it fails on the second command a stranger types, and the fix is small enough that "should" only delays it |
| C7a #541 | should, if C14 takes option (a) | it is the one place marvel alone loses finished work, and marvel alone can fix it in one path |
| C8 | should | contributors are the first adopters of an open repo, and every one of them gets about 1,800 read-only files per test binary |
| C15 | should; no seat holds blocker that I can see | a headless run on Ubuntu's tmux 3.4 is paid twice whenever an exit code is lost |
| C14-scope | (a), single-host core | the fleet's own daily use is the bus, shift and max-age, so the first stable release would not promise the thing its makers rely on |
| C1-name | alpha keeps `marvel` for the rc; stable takes `marvel` at `v0.1.0`, and alpha moves to `marvel-alpha` | every host on the tap today tracks alpha, so `brew upgrade` at `v0.1.0` moves them from the alpha head to an older stable unless they switch first |

On C6: I move to should because Skeptic and Seat lifecycle are right that it is a loud refusal, not a false reading, and `describe` prints the key. I ask that it land before C1b anyway, since the quick start's next step is `capture`.

On C8's question: "should, before C1b" is not a class of its own. If it became one, every should with a champion would join it, and the rc would wait on a list of non-blockers by accretion. Put such items in the outcome as "land before the rc if ready", with no `blocks` edge.

On C1-name: a stranger types `brew install arcavenae/tap/marvel`, so at `v0.1.0` that name must give the stable release. A new stable name (Release engineer's option B, kept past the rc) means the README tells strangers to install a name that is not the obvious one, forever.

## 2. Can the ruling be predicted?

- **C14-scope: no.** The recorded principles point both ways. Gradual elaboration and independence (SOUL sections 2 and 7) favour (a): every component usable alone, and start with the simplest thing. The operator's stated goal of restarting this fleet from a marvel manifest favours (b) or (c). No recorded ruling scopes a release. The card must hold:
  - the three options;
  - the verbs each one puts inside the promise, as a list;
  - what each costs: C7a and #953 become blockers under (b), and most of C13 does under (c);
  - the dates each pushes the rc to;
  - a recommendation with its sell-by date.
- **C1-name: no.** No ruling names tap formulas or channel naming. The card must hold:
  - who is on the tap today, by channel;
  - the downgrade each option causes on `brew upgrade`, and the one-time note that prevents it;
  - which name the README tells a stranger to type;
  - a recommendation with its sell-by date.

## 3. (Skeptic only)

Not mine.

## 4. One wrong edge

**"C5-docs blocks C1b" is missing an edge into C5-docs: the C1-name ruling blocks it.** The README rewrite names the formula a stranger installs, and that name is what C1-name decides. If C5-docs lands before the ruling, it names the wrong formula or none. Split C5 three ways:
- C5-code (`marvel example`, the archive smoke test): no edges, land any time.
- C5-docs (the README): blocked by C1-name.
- C5-install (the tap check): blocked by C1b.
