# Probe brief: switching running seats to another subscription when one runs out

**Status:** open, partly answered (see Result, 2026-10-04). Opened 2026-10-03 by the operator: "can we identify and switch all running agents that use [the personal account] by injecting a /login one at a time and logging in with the browser to restore service using a different subscription (Claude Enterprise in this instance)".

## Question

When the subscription a cluster's Claude seats run on hits its usage limit, can marvel find the affected seats and move them to another subscription without respawning them, and with the operator doing each browser login?

## Measured so far (kinu, 2026-10-03, about 23:00Z)

1. **Which seats are hit.** 8 of the 30 Claude seats were showing the weekly-limit menu ("Stop and wait for limit to reset / Wait here, then continue automatically at Oct 6 at 9pm / Switch to usage credits"). These were supervisor and builder seats on several teams. The other 22 were idle at a prompt; they share the account, so they would hit the same limit on their next turn. Two codex seats are on a different provider and are out of scope. Method: `marvel capture` on each seat, then grep for the menu text.
2. **Where the credential lives.** All 30 marvel-spawned Claude processes have no `CLAUDE_CONFIG_DIR` set, so they use the default config directory. They share one credential store with the operator's own interactive session. Method: env key names only, read from `ps eww`; no values were printed except that one key, which was absent.
3. **One /login reaches the shared store.** The operator ran `/login` once in the director session and signed in to Claude Enterprise. Afterwards, `/status` injected into a seat that was already running (a filer seat) reported `Login method: Claude Enterprise account`, the Enterprise organization, and the Enterprise email. That seat never ran `/login` itself.

## Not yet known

- Does the seat's **API traffic** move to the new credential, or does `/status` read the store while requests keep using the token cached in memory? This is the decisive test: send one small prompt to a seat that was limited, after it picks "Stop and wait", and see whether it is served or limited again.
- If it is served: no per-seat `/login` is needed at all. The fix is one operator login, then nudging each limited seat past its menu.
- If it is not served: a per-seat `/login` is needed. Each one is an operator browser round trip, about 30 on kinu, because all seats share one store.
- Whether a seat respawn (a fresh process reads the store at startup) is the cheaper path than either.

## Open for the operator

- **Scope.** Should every seat move to Enterprise, or only the seats doing that organization's work, with the other teams' seats waiting for the reset? This is the operator's call about whose subscription covers which work; the probe only measures.
- **Reversal.** After the reset, does the operator want seats back on the personal subscription? Because the store is shared, that is again one login plus whatever the decisive test shows.

## Constraint met during the probe

Sending a raw Escape key to close a seat's `/status` panel was refused by the auto-mode classifier ("Interfere With Workloads"). I stopped there. that filer seat is left showing its `/status` panel ("Esc to cancel"), and the operator closes it by hand.

## Later evidence (2026-10-04, about 01:10Z): leans toward "not switched"

Seats that were serving work after the operator's Enterprise `/login` went on to hit the limit. two supervisor seats sent at 23:01Z and 23:09Z; both now sit on the same menu, which offers to continue at the personal subscription's reset (Oct 6, 9pm). If their requests had moved to the Enterprise credential, a new limit on the personal plan would not stop them. This is indirect: an Enterprise limit with that reset time would look the same. The decisive one-prompt test is still the clean answer.

## Result (2026-10-04), reported by the director, not re-measured here

- All 30 kinu seats were moved to the other subscription by a per-seat `/login`. Most completed from the shared browser session; 3 needed the operator at the browser. So the "not served" branch above, where each seat needs its own `/login`, is what happened in practice.
- The decisive test was not run cleanly: whether a seat's requests switch to the new credential without a `/login`, by sending one small prompt to a limited seat after it picks "Stop and wait". No clean answer to "does one `/login` reach the in-memory token".
- The later evidence above still leans toward "not switched": seats that kept working after the operator's own `/login` later hit the personal limit.
- Not resolved by this result: whether a respawn would have been cheaper than 30 logins, the scope question, and reversal after the reset.

The result is the director's account. This PR records it; nothing in it was re-run, and no seat was touched to write it.
