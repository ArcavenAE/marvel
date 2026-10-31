# Inputs (chair's summary, checked 2026-10-31)

## 1. A host memory incident, 2026-10-31 (the busiest fleet host, read-only investigation by the supervisor seat)

- The macOS file-event daemon (fseventsd) held 27 to 29 GiB, mostly in swap, and one full core for hours. A jetsam report at 17:29Z named it the largest process at 86.5 GiB resident. Swap stood at 52.2 of 53.2 GB.
- Churn found under the temp directories in 24 hours: 695 leaked `marvel-test-home-*` directories (about 1801 files each in recent ones), 1531 `/tmp/marvel-h-*` harness-home bases, and about 3363 temp directories from an unrelated tool.
- 11 orphaned codex app-server processes (parent pid 1), 17 to 35 days old, with homes under `marvel-h-*`.
- 35 claude seat processes, 19.6 GB RSS in total; the oldest are 27 to 34 days old and run harness builds several weeks behind current.
- The leading file-event source in a 30 s root trace was git, consistent with harness sessions polling git status over a tree with about 280 worktrees.
- The graphical session on the host was lost twice in two days, which logged out keychain-backed harness logins until the operator logged in again.
- Hypothesis, not a finding: trees with hundreds of thousands of short-lived files feed the file-event daemon's history.

## 2. What exists on main (fa228925) that bears on it

- `marvel reap --homes` (#1157), the reap of a deleted seat's home and codex daemon (#1159), and `marvel reap --empty-bases` (#1160). All are operator-run, dry run by default.
- `internal/homeguard` makes a `marvel-test-home-*` directory per test binary with `os.MkdirTemp`. Nothing removes one when a test binary is killed.
- No host-health watch: nothing in marvel samples swap, memory pressure, temp-dir file counts, or orphaned processes. Cleanup on hosts has been by pattern, by hand.
- Leaked test brokers: marvel#884 (a killed go test run leaves its nats-server) and marvel#1162 (two reexec e2e tests). One such broker was live on another fleet host today.
- A draft from another architect seat proposes a watcher inside the daemon for host checks. Its text joins round 2 when the supervisor relays it.
- Suspend and resume is cluster-only, with no CLI verb (docs/design/fleet-suspend-resume.md, section 7, records team and seat scope as phase 2).
- Release mechanics: `release.yml` and `release-verify.yml` exist. Tags are `alpha-<date>-<sha>` only (749 tags, no `v*` tag). README says "the first stable release is pending".

## 3. The open issues

164 open: 73 bugs (`type.bug` or `bug`), 36 features, 19 tests, 9 RFCs. 107 were opened in October. Each seat read them with `gh issue list --repo ArcavenAE/marvel --state open --limit 200`.
