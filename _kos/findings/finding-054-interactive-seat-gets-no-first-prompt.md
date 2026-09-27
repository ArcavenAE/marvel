# finding-054: an interactive seat gets no first prompt, because runtime.prompt is headless-only

Work: harvest placement, 2026-09-27. The observation came from another
team's seats on 2026-09-26 and was relayed through the harvest
distribution manifest (rows 9 to 18). Verified by reading marvel at
origin/main a652fdf.

**Placement.** marvel is the subject: its adapters decide what a spawned
harness is told first.

## Observed

After every marvel kill in one fleet, the respawned interactive seat sat
at its start screen until an operator nudged the pane. Each seat needed a
nudge to begin.

## Mechanism

`runtime.prompt` is a manifest field for every mode, but only the headless
path reads it. The claude adapter appends it as the positional argument
only when the role is headless. The codex adapter's interactive branch
returns before reading it. Apply does not warn. So an interactive role
cannot declare a first message, and a respawn, shift or restart starts the
harness idle.

Both harnesses accept a positional prompt in interactive mode, so the
launch argument is a channel that exists. It is also safer than a follow-up
send-keys, which truncates long text at the head (#317, finding-040).

## Filed

- marvel#391 (the defect). Related: #317.
