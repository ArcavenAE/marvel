# Pattern sets

`<harness>/<harness version>/<pattern id>.yaml`, each beside the sample it was
built from (`<pattern id>.sample.txt`). A pattern set is added only from a
captured sample (docs/design/harness-state-watchdog-p1.md section 4). One
ships today: `claude/2.1.290/logged-out`, captured under probe P-WD1. A harness
version with no set reads `unknown`, and the watchdog reports nothing for it.
