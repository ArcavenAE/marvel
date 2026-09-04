# biscuit-go and Cedar-in-Go: viability for step 5+

**Task:** task 7 of `docs/studies/agent-identity-landscape.md` §11 — "Verify the
two deferred-format assumptions, no code." Independent, unblocked, and gates any
step-5 design.
**Scope:** does the identity lane's step 5+ (token exchange, attenuation, the
`Binding` seam) get to assume `biscuit-go` and/or Cedar in Go?
**Date:** 2026-09-04. Every "current" claim below is as of this date and rots.
**Repo pin for the internal check:** marvel `main` @ `440194b`. (HEAD advanced to
`deadd7e` under concurrent sessions while this was written; re-checked — those
commits touched neither the build config nor `docs/studies/`, so every internal
claim below holds at both.)
**Status:** no marvel code changed, no ticket filed, the parent study not edited.

---

## 1. Headline

**The two assumptions fail in opposite directions, and the parent study got the
Cedar one backwards.**

- **Cedar in Go is real, native, first-party, and cgo-free.** The study's flagged
  extrapolation — *"Cedar's implementation is Rust. A Go daemon adopting it means
  cgo or an out-of-process sidecar"* — is **wrong**. `cedar-policy/cedar-go` is a
  from-scratch Go implementation maintained in the same GitHub organization as
  the Rust reference. I built marvel's exact posture against it (`CGO_ENABLED=0`,
  cross-compiled to `linux/amd64`) and ran it. **Cedar does not collide with the
  zero-CGo posture at all.** The study's *other* reason to defer Cedar — marvel
  needs nineteen methods in three buckets, not a policy engine — survives intact
  and is the real one.

- **Biscuit in Go is a v3.0-level library with no functional change merged in
  ~19 months, and it does not implement the one feature the study wanted it
  for.** The study's case for biscuit was third-party blocks ("a peer marvel
  accepting a delegation without a bridge"). The Biscuit project's own feature
  matrix marks Go **❌** for third-party blocks, and the symbol is absent from
  the library's entire exported API. The parent study's verdict — "right shape
  for step 5+" — is right about the *format* and wrong about the *availability*.

**The net effect is a swap.** Cedar was deferred partly for a reason that does
not exist; biscuit was held open for a capability that does not exist in Go.
Neither changes the golden path, which needs no new cryptography — but it does
change which door is cheap to open later, and that is the decision this document
is for.

**One-line verdicts:**

| | Verdict |
|---|---|
| **Cedar (`cedar-go`)** | **VIABLE AND CHEAP — defer on need, not on cost.** Native Go, no cgo, Apache-2.0, first-party, releasing every few weeks. Its conformance corpus is ~3.5 months stale and its own drift alarm is broken (§3.4). |
| **Biscuit (`biscuit-go`)** | **NOT VIABLE for what step 5+ wanted it for.** No third-party blocks, no `check all`, no scopes, no snapshots; last functional merge 2025-02-11; no tagged release at its current module path. First-party *attenuation* does work. |
| **CGo** | **Does not bite. Neither library uses cgo.** Zero cgo-using packages in the full transitive closure of both, verified by build and by `go list -deps`. |

---

## 2. Method, and what class each claim is

The parent study is careful to separate code-derived claims from ticket prose. The
equivalent split here, because it is the difference between this document being
useful and being another layer of plausible inference:

- **EXECUTED (strongest).** I wrote a throwaway Go module in the session
  scratchpad importing both libraries, resolved it with the real module proxy,
  built it with `CGO_ENABLED=0` both natively (darwin/arm64) and cross-compiled
  (linux/amd64), ran it, and enumerated `biscuit-go`'s exported API with
  `go doc -all`. Nothing was executed inside the marvel checkout and no marvel
  file was touched.
- **PRIMARY-SOURCE (strong).** GitHub REST API against the canonical repos:
  releases, tags, commit dates, merged-PR history, open issues, workflow run
  conclusions and per-step outcomes, file contents (`go.mod`, `README.md`,
  `DEVELOPMENT.md`, workflow YAML). These are the projects' own records, read
  today, not summaries of them.
- **DOCUMENTARY (adequate).** Project READMEs and the Biscuit specification
  repository's own feature-support matrix — the maintainers' statements about
  their own software.
- **EXTRAPOLATION (labelled at every point of use).** Marked inline. There are
  three, and all three are marked.

Per `.claude/rules/upstream-claim-gate.md`: none of this has been published to
either project and none of it may be without running the gate first. Two things
below would be worth filing upstream — a broken notification step and a module
path that cannot be `go get`'d at its released tag — and neither should be filed
off the back of this document alone.

---

## 3. Cedar in Go

### 3.1 Does a real, maintained Go implementation exist? — Yes, and it is first-party

| | |
|---|---|
| Repository | `github.com/cedar-policy/cedar-go` |
| Licence | Apache-2.0 |
| Relationship to reference | **Not** the reference (that is the Rust `cedar-policy/cedar`), but **not a third-party port either** — it lives in the same GitHub organization, under the same `Cedar Contributors` copyright, with a DCO sign-off requirement |
| Latest release | **v1.8.0, 2026-06-01** |
| Release cadence | v1.5.0 (2026-02-10) → v1.8.0 (2026-06-01): 8 releases in ~4 months |
| Last commit on `main` | 2026-06-01 |
| Top contributors | `patjakdev` (319), `philhassey` (307), then Cedar-team and vendor contributors including strongdm and Google |
| Governance | Cedar joined the **CNCF as a Sandbox project** (announced ~2025-12-15); originally architected by AWS, developed in public with an RFC process |

This is the distinction the task asked for, and it lands on the good side: a
vendor-and-foundation-maintained implementation, not a hobby port of a security
primitive.

### 3.2 Is it complete for what marvel would need? — Yes for evaluation; validation is experimental

The README states the comparison against Rust directly. Verbatim:

> The Go implementation includes:
> - the core authorizer
> - JSON marshalling and unmarshalling
> - all core and extended types (including RFC 80's datetime and duration)
> - integration test suite
> - schema parsing and programmatic construction
>
> The Go implementation does not yet include:
> - CLI applications
> - the schema **validator** (experimental support is provided in x/exp/schema — please give us feedback!)
> - the formatter
> - partial evaluation
> - support for **policy templates**

Against the task's checklist:

- **Policy evaluation — YES, native and complete.** Executed: parsed a Cedar
  policy from source and authorized a request, returning `allow`.
- **Schema validation — EXPERIMENTAL ONLY.** It lives in `x/exp/schema`, and the
  README states that `x/exp` "is not subject to the semantic versioning
  constraints of the rest of the module and breaking changes may be made at any
  time." Schema *parsing* and programmatic construction are stable; *validation*
  is not.
- **Native / CGo / RPC — NATIVE.** See §5.
- **Two gaps worth naming even though marvel does not need them today:**
  **policy templates** are absent, and **partial evaluation** is absent. Templates
  are Cedar's mechanism for parameterizing one policy across many principals; if
  marvel's authorization ever becomes per-principal rather than per-grade, that
  absence becomes load-bearing. *(Extrapolation, labelled: that marvel would want
  templates in that scenario is my inference, not something Cedar documents.)*

### 3.3 Version and spec drift — the interesting part

cedar-go versions itself independently of the Cedar language (cedar-go v1.8.0;
Rust `cedar` v4.12.0, 2026-07-28), so the tag numbers say nothing. The real
conformance link is a **vendored corpus of integration tests generated from the
Rust implementation**, plus a nightly job that checks the vendored copy against
upstream.

That mechanism is unusually good, and right now it is not working.

| Fact | Evidence |
|---|---|
| cedar-go's vendored `corpus-tests.tar.gz` was last updated **2026-03-19** | commit history for that path |
| Upstream regenerated it **three times since**: 2026-05-18 ("4.11"), 2026-07-24 ("4.12.0"), 2026-09-02 | `cedar-policy/cedar-integration-tests` commit history for that path |
| The nightly comparison last succeeded **2026-05-18**; it has failed on **every run since** | 100/100 runs on the most recent API page are `failure`, spanning 2026-05-28 → 2026-09-04; the newest `success` anywhere in the last 4 pages is 2026-05-18T01:04Z |
| On the three most recent runs, **both** the `Compare` step **and** the `Notify on Failure` step conclude `failure` | per-step job outcomes for runs `33822955019`, `33700943545`, `33576690992` |
| Consequently **no issue was ever opened**. No open issue carries the `upstream-corpus-test` label; the most recent one, #110, was opened 2025-10-02 and closed 2026-03-22 | issue search by label, state `all` |

**What this does and does not mean.** It means cedar-go's machine-checked
conformance evidence is pinned to a Cedar 4.10-era corpus and has not been
refreshed across two Cedar minor releases, and that the alarm designed to tell
the maintainers so is itself broken. It does **not** mean cedar-go is
semantically wrong: its own CI (`build_and_test`) is green, and I did not
determine what the corpus differences are or whether cedar-go would fail the
current corpus. That check requires running `make corpus-update && make test`
against the repo, which is outside this task's no-code scope.

Two pieces of context that argue against reading this as neglect. cedar-go's own
`DEVELOPMENT.md` warns that "The Cedar team does occasionally make breaking
changes to the format of the tests, so bringing them up to date may be a
non-trivial amount of work" — this is a known-hard sync, not an ignored one. And
the corpus is not their only conformance channel: the v1.8.0 release notes
describe two behavior changes made specifically "to bring it in conformance with
the Rust implementation," with an explicit upgrade warning. Divergences are being
found and fixed through other routes.

**Fair summary: the drift is in the currency of the conformance fixtures, not a
demonstrated evaluation defect.** For marvel's plausible use — nineteen method
names against three grades — the blast radius of a 4.10-vs-4.12 corpus gap is
close to nil. For an operator-authored policy corpus using datetime extensions
and edge-case IP semantics, it would not be.

### 3.4 What would marvel gain over the golden path? — Nothing at step 5. Something later.

The golden path's check is `(principal, action, resource, context) → allow/deny`
over 19 methods sorted into `observe` / `mutate` / `self-report`. Cedar buys:
externalized operator-authored policy, a formally-modelled evaluator (verified in
Lean, differentially tested against Rust), schema-checked entity models, and
analyzability.

Marvel at step 5 has a table with three rows. **Adopting Cedar there would be a
policy engine whose policy file says "supervisors may mutate."** The parent
study's judgment is correct and this document does not disturb it.

What *changes* is the cost of the door. The study deferred Cedar partly because
adopting it looked like accepting cgo or standing up a satellite process — a
architectural decision requiring the 2026-08-01 language ruling. It is not: it is
`go get`, one pure-Go module, two dependencies. **The deferral should be recorded
as "not needed yet," never as "expensive," because the second is false and a
future reader will act on it.**

---

## 4. Biscuit in Go

### 4.1 Does a real, maintained Go implementation exist? — It exists; "maintained" needs qualifying

| | |
|---|---|
| Repository | `github.com/eclipse-biscuit/biscuit-go` (**moved** from `biscuit-auth/biscuit-go`; the Biscuit project is now under the Eclipse Foundation) |
| Licence | Apache-2.0 |
| Relationship to reference | Not the reference (that is `biscuit-rust`); it is the project's own Go implementation, in the project's org — again, not an outside port |
| Latest tag | **v2.2.0** — and there are **no GitHub releases at all**, only tags |
| Last functional code merge | **2025-02-11** (#157, "fix(grammar): use right queries on deny policies") |
| Everything merged since | #165 copyright headers (2025-04), two dependabot bumps (2025-05, 2025-08), #175 org rename (2025-10), #179 add SECURITY.md (2026-07) |
| Open issues | 26, including #155 "Publish a new release of the Go module" (**open since 2025-01-13**) and #128 "Different functional behavior between the Rust library and the Go module" (**open since 2023-10-14**) |

**~19 months with no functional change merged.** The repository is not abandoned —
someone moved it to Eclipse and added a security policy — but nobody is building
on it.

**A concrete packaging defect, executed rather than inferred.** The org move
broke the module path at every released tag:

```
$ go get github.com/eclipse-biscuit/biscuit-go/v2@v2.2.0
go: ... parsing go.mod:
        module declares its path as: github.com/biscuit-auth/biscuit-go/v2
                but was required as: github.com/eclipse-biscuit/biscuit-go/v2
```

`main`'s `go.mod` declares the new path, but **no tag carries it**. A consumer
must choose between the last real tag under the legacy path
(`github.com/biscuit-auth/biscuit-go/v2@v2.2.0`, which does resolve) and an
untagged pseudo-version at the current path
(`github.com/eclipse-biscuit/biscuit-go/v2@main` →
`v2.2.1-0.20260713182043-16cbdd78ca32`). Both work; neither is a tagged release
at the canonical import path. That is the visible consequence of issue #155
sitting open for twenty months, and for a security primitive it is a supply-chain
wart, not a cosmetic one.

### 4.2 Is it complete for what marvel would need? — No, and precisely not

The Biscuit specification repository maintains a per-implementation feature
matrix. The Go column, verbatim from `eclipse-biscuit/biscuit/README.md`:

| | Rust | Haskell | Java | **Go** | Python | C# |
|---|---|---|---|---|---|---|
| **v3.0** | ✅ | ✅ | ✅ | **✅** | ✅ | ✅ |
| **v3.1** | ✅ | ✅ | 🚧 | **❌** | ✅ | ✅ |
| scopes | ✅ | ✅ | ✅ | **❌** | ✅ | ✅ |
| check all | ✅ | ✅ | ✅ | **❌** | ✅ | ✅ |
| bitwise operations | ✅ | ✅ | ✅ | **❌** | ✅ | ✅ |
| snapshots | ✅ | ❌ | 🚧 | **❌** | ✅ | ❌ |
| **v3.2** | ✅ | ✅ | 🚧 | **❌** | ✅ | ✅ |
| **third party blocks** | ✅ | ✅ | 🚧 | **❌** | 🚧 | ✅ |
| **v3.3** | ✅ | ✅ | 🚧 | **❌** | ✅ | ✅ |

The same README's "How to help us?" section asks contributors to "add support for
biscuit v3.2 to java and go implementations." The current spec tag is **v3.3
(2024-12-17)**. Go is two minor spec versions behind and the gap is not closing.

Corroborated three independent ways:

1. **The project's matrix** (above).
2. **The maintainers' own umbrella issue** `#117 "Biscuit v3 support"` — open
   since 2023-04-17, **last updated 2024-02-12**, with `3rd party support`,
   `check all support`, `add bitwise operators`, `add !=`, `querying an
   authorizer with scopes`, and `authorizer snapshots` all still unchecked. The
   issue itself notes: "Implementing 3rd party blocks will require a lot of
   changes in the datalog engine."
3. **EXECUTED — the exported API.** `go doc -all` over the package (360 lines of
   public surface) contains **zero** occurrences of `ThirdParty`, `External`,
   `Scope`, `CheckAll`, `Snapshot`, or `Bitwise`. There is exactly one `Append`
   method, the ordinary first-party one.

Against the task's checklist:

- **Offline attenuation — YES, and it works.** `(*Biscuit).Append(rng, block)`
  plus `Seal`. Executed: minted a 169-byte token from an ed25519 root key through
  the datalog parser.
- **Third-party blocks — NO.** Absent from the matrix, from the umbrella issue's
  checklist, and from the API.
- **Datalog checker — YES, at v3.0 level.** The parser, authorizer and datalog
  engine exist and function. But `check all`, scopes, and bitwise operators are
  missing, and there are open issues for `!=` (#139-adjacent), lazy boolean
  operators (#138), heterogeneous equality (#140), `.type()` (#141), and
  nullability (#143) — all filed 2024-05-12, none closed. There is also an open
  **correctness** issue, #180 "Datalog world timeout is spurious" (2026-08-24),
  which is the newest substantive activity on the repository.
- **Key rotation — YES.** `WithRootKeyID(uint32)`, `(*Biscuit).RootKeyID()`, and
  `AuthorizerFor(keySource PublickKeyByIDProjection, ...)` support root-key
  selection by ID; `RevocationIds()` is present. This landed in #151 (2024-11-13)
  and #154 (2025-01-13) — the last substantive features merged. *(The typo in the
  exported identifier `PublickKeyByIDProjection` is cosmetic and I note it only
  as a signal about review depth on a security library's public API.)*

**No viable alternative exists.** A GitHub search surfaced no other Go Biscuit
implementation of any substance; `eclipse-biscuit/biscuit-go` (90 stars) is the
only one. Designing around it means implementing Biscuit, not choosing a
different library.

### 4.3 What would marvel gain over the golden path? — And here the two gaps cancel

Biscuit buys two distinct things, and the parent study wanted both:

1. **First-party offline attenuation** — a supervisor narrows a token for a
   worker without a round trip to the daemon. **`biscuit-go` has this today.**
2. **Cross-domain delegation via third-party blocks** — a peer marvel accepts a
   delegation with no ahead-of-time key consolidation. **`biscuit-go` does not
   have this, and nobody is building it.**

The useful observation is that **capability (2) is exactly the marvel need the
parent study already deferred for lack of evidence.** Its §C could not answer
whether peer federation is hierarchical or mutual, because that requires a second
deployment and M5 is unbuilt. So the missing Biscuit feature and the unanswerable
marvel question are the same question, and they cancel: marvel cannot specify
what it would do with third-party blocks, and Go could not offer them if it
could.

Capability (1) is available — but the study's §D is equally clear that nothing in
marvel delegates today, and a token format whose defining feature is unused buys
the cost with none of the benefit. That reasoning is untouched by anything here.

---

## 5. The CGo question, answered by building

Marvel's zero-CGo posture is not prose — it is enforced in two places, verified at
`440194b`:

```
.goreleaser.yml:15:      - CGO_ENABLED=0
.github/workflows/ci.yml:102:          CGO_ENABLED: "0"
```

So the question is falsifiable by compiling. I built a module importing **both**
libraries and:

| Check | Result |
|---|---|
| `CGO_ENABLED=0 go build` (darwin/arm64) | **OK** |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build` | **OK** |
| `CGO_ENABLED=0 go run .` | **OK** — `cedar authorize: allow`, `biscuit minted bytes: 169` |
| `go list -deps` for packages with `CgoFiles`, linux/amd64 | **0** |
| `import "C"` anywhere in either repo (code search) | **0** in both |

The transitive module closure of both libraries together is 18 modules, all pure
Go: `golang.org/x/exp`, `go-cmp`, `participle/v2`, `testify`, `protobuf`, and
their dependencies.

**One thing that looks like a CGo problem and is not.** `cedar-go` contains Rust
under `test/cedar-entity-parsing-tool/` and `test/cedar-validation-tool/`, each
with a `Cargo.toml`, and dependabot raises cargo PRs against them. These are
**test-fixture generation tools**. They are not linked, not invoked at runtime,
and not reachable from the Go module graph — the build above proves it, since a
runtime dependency on them could not cross-compile to linux/amd64 with cgo
disabled. A future reader who greps `cedar-go` for "Rust" will find this and
should not re-derive the parent study's conclusion from it.

**Verdict: CGo does not bite, for either library.** The parent study's §6
extrapolation and its §11 task-7 framing ("Cedar's Go integration story against
marvel's zero-CGo posture") both anticipated a collision that does not exist.

---

## 6. What I could not establish

Stated rather than filled with inference:

- **Whether cedar-go actually passes the current upstream corpus.** I established
  that its vendored copy is stale and that the drift detector is firing and
  silently failing to notify. I did not run `make corpus-update && make test`,
  which is the only thing that answers whether the staleness has behavioral
  consequences. That is a code task and this was scoped no-code.
- **Why cedar-go's `Notify on Failure` step fails.** The step conclusion is
  `failure` on every run I checked; the cause is not in the data I read. *(The
  workflow assigns new issues to three named users, and an assignee who is no
  longer a repository collaborator would make `issues.create` fail — that is
  **extrapolation** from reading the workflow YAML, not something I verified.)*
- **Whether the Biscuit project intends to close the Go v3 gap.** #117 has not
  been updated since 2024-02-12 and has zero comments. Absence of a plan is not a
  statement that there is no plan.
- **Whether biscuit-go's v3.0-level datalog engine is correct.** Open issue #180
  reports a spurious timeout in the datalog world and #128 reports behavioral
  divergence from Rust, unresolved since 2023. I read the issues; I did not
  reproduce either.
- **Semantic equivalence of either library to its reference implementation.** No
  differential testing was performed here. Cedar has a mechanism for this (the
  corpus); Biscuit's Go implementation has a samples suite with skips, which I
  did not run.

---

## 7. Recommendation

**Step 5+ may assume Cedar-in-Go if it ever wants a policy engine. It must not
assume biscuit-go at all.**

Concretely:

1. **Record Cedar as available-and-deferred, and correct the reason.** The
   parent study's §6 Cedar entry should be read with its cgo/sidecar sentence
   struck. Defer Cedar because marvel has 19 methods and 3 grades, which is a
   table; not because it is architecturally expensive, which it is not. The
   study's free take-aways stand and are now cheaper: write the check's signature
   as `(principal, action, resource, context) → allow/deny` so Cedar is a
   drop-in, and do not hand-roll an authorization DSL when a Lean-verified,
   differentially-tested, CNCF-sandbox, pure-Go one is `go get` away.

2. **Do not design step 5+ around biscuit-go.** The cross-domain half of the
   study's case for Biscuit is unavailable in Go and has been for three years,
   and the one need it served — peer-marvel federation — is itself blocked on a
   second deployment that does not exist. This is a "defer the question," not a
   "design around it": there is nothing to design around until marvel can say
   what it would delegate.

3. **Keep the study's cheap provision, which this makes more valuable, not
   less.** §6's recommendation to keep the principal's credential field **opaque
   and format-versioned** was already right. It is now the whole mitigation: it
   lets the format decision be deferred past a technology that is not ready,
   at zero cost today, with no envelope schema change when it is.

4. **If first-party attenuation alone turns out to be the step-5 need** — a
   supervisor narrowing a worker's token inside one trust domain, with no
   cross-domain hop — then re-open the biscuit question specifically, because
   `biscuit-go` *does* implement that. Pin the legacy module path
   (`github.com/biscuit-auth/biscuit-go/v2@v2.2.0`) or an explicit pseudo-version,
   never a floating `main`, and treat the ~19-month functional freeze and open
   datalog-correctness issue #180 as the risks they are. This is a narrower and
   much better-supported claim than "adopt biscuit at step 5."

5. **Do not file anything upstream off this document.** Two things here would be
   legitimately useful to both projects — cedar-go's broken drift notification,
   and biscuit-go's unresolvable module path at its released tag. Both are
   falsifiable claims about other people's software and both are
   `.claude/rules/upstream-claim-gate.md` territory. If either is filed, run the
   gate and re-verify at that moment; the workflow-run facts in §3.3 in particular
   are a moving target that a maintainer could fix tomorrow.

### The single piece of evidence that would change this

**For Cedar:** a marvel authorization requirement that is not a table — per-team
operator-authored policy, per-principal rules, or anything needing schema
validation of an entity model. That flips Cedar from "defer" to "adopt," and §5
means the adoption is `go get`, not an architecture decision. *(If it is schema
validation specifically, note that cedar-go's validator is in `x/exp` with no
semver guarantee — that is the one place the Go implementation is genuinely
behind.)*

**For Biscuit:** a merged PR closing the `3rd party support` checkbox in
`eclipse-biscuit/biscuit-go#117`, or a Go entry flipping from ❌ to ✅ under
**third party blocks** in the Biscuit spec repository's feature matrix. Either
would reopen the whole question. A tagged release at the `eclipse-biscuit` module
path would not, by itself, be enough — it would fix the packaging defect without
touching the capability gap.

---

## Sources

**Internal (verified at marvel `440194b`):**
`marvel/docs/studies/agent-identity-landscape.md` §6, §10, §11 task 7, §12;
`.goreleaser.yml:15`; `.github/workflows/ci.yml:102`;
`marvel/_kos/probes/decision-brief-kxce-substrate.md:77`;
`marvel/_kos/nodes/bedrock/elem-tmux-session-substrate.yaml`;
orc `vision.md:311`, `docs/roadmap.md:148`, `docs/marvel-remap-2026-08.md:29`;
`.claude/rules/upstream-claim-gate.md`.

**Primary-source, read 2026-09-04 via the GitHub REST API:**
`cedar-policy/cedar-go` (repo metadata, releases v1.5.0–v1.8.0, tags, commits,
contributors, `go.mod`, `README.md`, `DEVELOPMENT.md`,
`.github/workflows/corpus.yml`, workflow runs for `corpus.yml` and
`build_and_test.yml` incl. per-step outcomes, issues by label
`upstream-corpus-test`);
`cedar-policy/cedar` (releases, incl. v4.12.0 2026-07-28);
`cedar-policy/cedar-integration-tests` (commit history for `corpus-tests.tar.gz`);
`eclipse-biscuit/biscuit-go` (repo metadata, tags, commits, merged PRs, open
issues incl. #117 body, #128, #155, #180, `go.mod`, `README.md`);
`eclipse-biscuit/biscuit` (repo metadata, tag v3.3, `README.md` feature matrix).

**Executed (session scratchpad, Go 1.27.0 darwin/arm64):** module resolution via
the Go module proxy for `cedar-go@v1.8.0`,
`biscuit-auth/biscuit-go/v2@v2.2.0` and `eclipse-biscuit/biscuit-go/v2@main`;
`CGO_ENABLED=0` build (darwin/arm64 and linux/amd64), run, `go list -deps`
cgo audit, `go list -m all`, `go doc -all` API enumeration.

**Documentary:**
[Cedar Joins CNCF as a Sandbox Project](https://aws.amazon.com/blogs/opensource/cedar-joins-cncf-as-a-sandbox-project/);
[Biscuit 3.0](https://www.biscuitsec.org/blog/biscuit-3-0/);
[Third-party blocks: why, how, when, who?](https://www.biscuitsec.org/blog/third-party-blocks-why-how-when-who/).
`biscuitsec.org`'s own implementation-status page returned HTTP 403 to automated
fetch; the specification repository's README matrix was used instead, and is the
more authoritative of the two anyway.

**Nothing in this document was published to either upstream project.**
