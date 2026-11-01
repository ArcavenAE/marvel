# Companies: installed definition libraries, and the cast pinned to one

Status: design, for a ruling. Nothing here is built.

## Why

A team's roles today get their prompts from whatever a launch script reads at spawn. marvel cannot say which library a running team came from, at which version, or whether two teams on one cluster run the same text. Rolling a library forward or back means editing files on each host. This design gives marvel a native object for the library, the **company**, and lets each team pin one version. marvel owns the reading, the pinning and the record. It owns no install step, so it still works with nothing but its own binary.

Vocabulary (ruled): a **company** is a library of team, workflow and agent definitions. The **cast** is the set of teams running on one cluster, each drawn from a company. "Pack" is not a marvel word.

## The version model

- A company is identified as `name@version`. A version is an opaque string the installer chooses (a date and a commit, a semver, a tag). marvel orders nothing by it.
- marvel reads companies from a **company store**: `$XDG_DATA_HOME/marvel/companies/<name>/<version>/`, default `~/.local/share/marvel/companies`. The config key `companies.roots` adds more read-only roots. The store sits outside `~/.marvel`, so no installer writes into marvel's own home.
- An optional `current` symlink in `<name>/` names the version that name resolves to when a manifest asks for the name without a version.
- Several names and several versions coexist. Nothing is ever resolved from a working tree.
- **builtin.** marvel embeds one company in the binary, as it already embeds `marvel example`. It is always present, takes no store entry, and is what a manifest gets when it names no company. `marvel company export builtin <dir>` writes a copy for someone who wants to fork it.

### Layout of a version

```
<name>/<version>/
  company.toml        name, version, schema = 1, source (free text), definitions index
  roles/<id>.md       role definitions
  teams/<id>.toml     team templates (optional)
  workflows/<id>.md   workflow definitions (optional)
```

`company.toml` lists each definition with a `status`: `ratified`, `proposal` or `retired`. marvel refuses to launch a `retired` definition, and launches a `proposal` only when the role sets `allow_proposal = true`.

## Pinning a team

```toml
[[teams]]
name = "core"
company = "acme-roles@2026.11.01-ecc28ac"        # exact pin
# company = "acme-roles"                          # follows the store's current

[[teams.roles]]
name = "builder"
definition = "role/builder"
scope = "marvel"                                  # a parameter, not a role
```

- **Exact pin** (`name@version`): reproducible. Apply refuses a pin that is not in the store.
- **Name only**: the team follows the store's `current`. This is what lets a library roll forward with no edit at the host: the installer flips `current`, and the next launch or shift picks it up.
- Either way, the version is **resolved once per launch** and recorded on the session. A running session never changes version; only a new launch or a shift moves it.
- A role with no `definition` behaves as today. Nothing existing changes until a manifest opts in.

## Master and reference copies

- The **master** is the source the library is edited in, outside marvel, by whatever review its owner uses. marvel never reads it.
- Each store version is an **immutable reference copy**. marvel never writes one.
- At resolve, marvel computes a content hash over the version's tree. The first time it sees `name@version` it records the hash in its own state. A later resolve whose hash differs is refused, as is a version whose tree is writable by the daemon's user. These are structural refusals, not health checks.
- A seat runs from no copy of its own. marvel renders the prompt from the pinned version at launch, passes it to the harness, and stamps its hash on the session (the existing `marvel.prompt_artifact.sha256`). The seat's working directory is its `workdir`, never the store. So no seat can edit what it runs from, and nothing needs a per-seat copy.

## Verbs marvel owns

| verb | does |
|---|---|
| `marvel get companies` | each `name@version` in the store: hash, source, whether it is `current`, which teams use it |
| `marvel describe company <name[@version]>` | its definitions and their status |
| `marvel get cast` (#946) | the running teams, each with `name@version` and hash as resolved at launch |
| `marvel work` / `apply` | refuses a missing pin, a hash mismatch, a writable version or a retired definition |
| `marvel doctor` | reports store problems, read-only |
| `marvel company export builtin <dir>` | writes the builtin company out, the only write marvel makes, and only to a path the user names |

marvel has no `install`, `remove`, `fetch` or `activate`. Anything that writes a valid version directory and, optionally, flips `current` is an installer: a package manager, `tar -x` of a release, or `git archive` by hand.

## What it replaces

Launch scripts that read a library at spawn and pass the slice in `--append-system-prompt`. With a company pinned, the claude adapter passes the rendered definition as its one system prompt (the same single-prompt rule as today, claude.go). Wrapper commands stay supported for anything else they do.

## Tests (for the builder's red/green PR)

- Given a team with no `company`, when it launches, then the prompt is the builtin's definition or today's behavior, and no store is read.
- Given `company = "x@1"` not in the store, when `marvel work` runs, then it refuses and names the store path.
- Given `x@1` recorded with hash H and its tree later changed, when a role launches from it, then the launch is refused and names both hashes.
- Given `company = "x"` and `current` flipped from 1 to 2 while a session runs, then that session's record still shows `x@1`, and the next launch shows `x@2`.
- Given a definition with `status = "retired"`, when a role names it, then apply refuses.
- Given any of the above, the daemon writes nothing under the store (sandbox profile denying writes outside scratch, with a blocked-write control).

## Open for the ruling

- Name-only pins as the default for fleet teams, with exact pins for experiments and rollback. I recommend this; it is what removes the per-host edit.
- The store path under `XDG_DATA_HOME` rather than `~/.marvel`.

This recommendation is valid until 2026-11-08; the architect re-checks it then.
