# Probe brief: does the transfer gap behind `marvel cp` survive remote `marvel work` and `get -o`, or is there a second use case?

**Status:** DRAFT 2026-10-24, not filed, not run. Written for the architect
from the idea `_kos/ideas/marvel-scp-verified-transfer.md` (2026-09-21).
Premises read at marvel `origin/main` 9672ca5 (the local checkout was behind
origin and was not used for reads).
**Question asked:** the idea proposes `marvel cp <src> <cluster>:<dst>` over
`mrvl://`, digest-checked, RBAC-gated per direction and path, with an
owner-external off switch. Before any design: is a general file channel the
missing piece, or did the incident that earned it have a path already?
**Medium:** reading marvel; two scratch daemons on one host, simulator
runtime, joined over `mrvl://` on localhost with scratch keys; a read-only
search of the orchestrator, director and marvel graphs for recorded cross-host transfer
cases. No real cluster, no real keys, no model calls.
**bd:** none for this idea [measured] `bd sql --json` title search for
"marvel cp", "file transfer", "scp". Related closed item: aae-orc-zv4jw
(`get team|workspace -o json|yaml`).

## 1. Question

For the one recorded case (a ~15KB manifest on one fleet host that a director on another
needed in order to run `marvel work`), does the existing remote apply path
carry the bytes intact to the target daemon, and are there other recorded
cases that only a general verified file channel would serve?

## 2. Hypothesis

`marvel work` already reads the manifest on the client and sends its bytes in
the `apply` RPC to whichever daemon `--cluster` selects, over `mrvl://`, so an
agent on the first host holding an admin key on the second could have applied the manifest
to the second without moving a file. The one defect on that path is that the
workspace root is resolved on the client from the manifest's own directory
unless the manifest declares an absolute root. No other recorded cross-host
case needs a general file channel; key material already has its own scoped
path. If that holds, `marvel cp` has no second use case today and the
manifest case is a remote-apply question.

## 3. Already built

| Item | State | Evidence |
|---|---|---|
| Manifest bytes travel in the RPC | built since 2026-03; "The bytes go up untouched" | [read] `cmd/marvel/main.go:1036-1058` (`manifest_data`); [measured] `git log -S manifest_data` first at `c8fe227`, 2026-03-16 |
| `--cluster` as a persistent flag over `mrvl://` | built 2026-04-13 | [read] `cmd/marvel/main.go:218`; [measured] `9abee5b` "mrvl:// protocol, cluster config, and documentation" |
| Workspace root resolved on the client | built; empty or relative declared root resolves against the client's file path; `~` gives none | [read] `cmd/marvel/workroot.go:19-34` |
| The complement: `get team\|workspace -o json\|yaml` | built and redacted; the desired section is the stored state after edits, "not the applied file, and no round trip to it is promised" | [read] `cmd/marvel/getoutput.go:13-21`, `:135-146`; aae-orc-zv4jw closed [measured] |
| `get sessions -o json` across clusters | built 2026-10-23 | [read] marvel `9fa825b` (#1039) |
| Per-key authority on `mrvl://` | two scopes: `admin` (every method) and `credential-push` (credential put, list, delete and the leaf toggle); an unknown scope may call nothing | [read] `internal/daemon/scope.go:9-26`, `:53-79` |
| Per-direction or per-path file policy, owner off switch for files | not found | [measured] `git grep` for `marvel cp`, file transfer, scp: one unrelated comment in `internal/rlog/rlog.go:68` |
| Reply size bound | 32 MiB per daemon reply | [read] `internal/daemonrpc/exchange.go:38` |
| director sim O-26 (the idea's citation) | not found on director main | [measured] `git -C director grep 'O-26'` and variants: no match |

The incident date is the same as the specimen in the no-control-bypass rule: on
2026-09-21 an ops-manifest fix needed `marvel work` on a live team and the
auto-mode classifier denied the relaying inject [read]
the no-control-bypass rule, in its account of that date. Whether the
transfer gap or the classifier was the binding constraint that day is not
established here (open question 2).

## 4. Method

Run conditions (operator ruling, 2026-10-14): `env -i` with a
scratch `HOME` and a short `TMPDIR` per daemon; no `MARVEL_SOCKET`; localhost
only under a `sandbox-exec` profile, with a refused-connection control showing
the profile's own "Operation not permitted"; the sandbox write proof (a
blocked write to a fixed path outside scratch, a successful write inside).
Cleanup stops only the daemons this run started, by their pid files, with the
`ps -p` check outside the sandbox that `brief-multiple-clusters-per-host.md`
section 3 uses.

- **R1 bytes intact.** Daemon A and daemon B under two scratch HOMEs
  (G-HOME, simulator runtime). B listens on a localhost `mrvl://` port; A's
  client holds a scratch key authorized on B with `admin` scope. Generate a
  manifest of about 15KB that declares an absolute workspace root under B's
  scratch tree. From A's side, run `marvel --cluster B work <file>`. Record
  the client's sha256 of the file and, on B, what can be read back: `get
  workspace -o yaml` and B's log for the apply. Record what readback can and
  cannot prove about byte identity (the stored state is not the file, by
  `getoutput.go:13-21`).
- **R2 root resolution.** Repeat R1 with no declared root and with a relative
  one. Record the `workspace_root` B received and whether the path exists on
  B's side.
- **R3 the gate.** Repeat R1 with a key authorized on B at `credential-push`.
  Record the refusal text.
- **S1 the second use case.** Read-only search of `_kos/findings`,
  `_kos/ideas`, director `sim/requirements.md` and `sim/` design notes in the
  the orchestrator, director and marvel for recorded cases of a file needed on another
  host. Classify each as manifest, credential, log or capture, or other, and
  cite each by path and line. Report the search terms and a positive control
  (the ops-manifest incident itself must be found).

## 5. Pass and fail

- **Supported** if R1 applies on B with the manifest's content intact as far
  as readback shows, R2 shows the client path reaching B when no absolute root
  is declared, R3 is refused, and S1 finds no recorded case in the "other"
  class.
- **Disconfirmed for the remote-apply half** if R1 fails or alters content
  over `mrvl://`; then the manifest case does need a transfer, and the idea's
  digest rule is the first requirement.
- **Disconfirmed for the "no second use case" half** if S1 finds at least one
  recorded case outside manifests and credentials; the finding then lists
  those cases as the requirement set for `marvel cp`.

## 6. Scope out

- Building `marvel cp`, a new scope, a policy file, or an off switch.
- Any real cluster, real key, or the operator's `~/.marvel`.
- Key material over any channel (ADR-009; the idea excludes it too).
- Changing how `workspace_root` resolves; R2 only records it.

## 7. Open questions

1. If R2 shows the client path reaching a remote daemon, is that a defect in
   remote `marvel work` or intended? It is a marvel design choice the brief
   does not make.
2. In the 2026-09-21 incident, did the agent on the first host hold a key on the second, and
   was the binding constraint the missing transfer or the classifier denial
   that the no-control-bypass rule records? Only the people or seats present can
   say.
3. Where is director sim O-26 recorded? The idea cites it and director main
   does not carry it [measured].
4. The idea's owner-external off switch and policy ceiling mirror
   `_kos/ideas/mrvl-proxy-nats-and-generic-relays.md`. If S1 finds a second
   use case, is that control designed once for both, or per channel? Not
   decided here.
5. The idea says RBAC per direction and path prefix. The current model is per
   key and per method, and its principal model waits on aae-orc-bs3x [read]
   `scope.go:53-59`. Does a file scope wait on that ticket?
