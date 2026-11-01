# Companies: installed definition libraries, and the cast pinned to one

Status: design, for a ruling. Nothing here is built.

## Why

A team's roles today get their prompts from whatever a launch script reads at spawn. marvel cannot say which library a running team came from, at which version, or whether two teams on one cluster run the same text. Rolling a library forward or back means editing files on each host. This design gives marvel a native object for the library, the **company**, and lets each team pin one version. marvel owns the reading, the pinning and the record. It owns no install step, so it still works with nothing but its own binary.

Vocabulary (ruled): a **company** is a library of team, workflow and agent definitions. The **cast** is the set of teams running on one cluster, each drawn from a company. "Pack" is not a marvel word.

## The version model

- A company is identified as `name@version`. A version is an opaque string the installer chooses (a date and a commit, a semver, a tag). marvel orders nothing by it.
- marvel reads companies from one or more **company roots**, each laid out `<root>/<name>/<version>/`. The roots come from the config key `companies.roots` in `~/.marvel/config.yaml`, never from the environment, so a daemon started by launchd or systemd resolves the same paths as a shell. With the key unset there are no roots, and only the builtin exists. marvel writes no root. `marvel doctor` prints the roots it read.
- **What marvel skips:** any entry whose name starts with `.` (an installer's staging and lock files), any `<version>` without a `company.toml`, and any `<name>` with no such version. A root can therefore be a general package store that also holds other content.
- An optional `current` symlink in `<name>/` names the version that name resolves to when a manifest asks for the name without a version.
- Several names and several versions coexist. Nothing is ever resolved from a working tree.
- **builtin.** marvel embeds one company in the binary, as it already embeds `marvel example`. It is always present, takes no root entry, and is what a manifest gets when it names no company. `marvel company export builtin <dir>` writes a copy for someone who wants to fork it.

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
# company = "acme-roles"                          # follows that name's current

[[teams.roles]]
name = "builder"
definition = "role/builder"
scope = "marvel"                                  # a parameter, not a role
```

- **Exact pin** (`name@version`): reproducible. Apply refuses a pin that is in no root, and refuses a name that two roots both hold, as ambiguous.
- **Name only**: the team follows that name's `current`. This is what lets a library roll forward with no edit at the host: the installer flips `current`, and the next launch or shift picks it up.
- Either way, the version is **resolved once per launch** and recorded on the session. A running session never changes version; only a new launch or a shift moves it.
- A role with no `definition` behaves as today. Nothing existing changes until a manifest opts in.

## Master and reference copies

- The **master** is the source the library is edited in, outside marvel, by whatever review its owner uses. marvel never reads it.
- Each installed version is an **immutable reference copy**. marvel never writes one.
- **The tree hash (v1).** Over every entry under `<version>/`, sorted by path bytes: one line of the relative path, the type (`f`, `d` or `l`), and for a file the sha256 of its bytes, for a link its target. The hash is the sha256 of those lines. An installer that records a hash uses this same definition, so the two can be compared.
- At resolve, marvel computes the tree hash. The first time it sees `name@version` it records the hash in its own state. A later resolve whose hash differs is refused and names both hashes.
- **Read-only:** no file and no directory in the version tree carries any write bit. A writable directory would let an entry be added or renamed, so directories count. marvel refuses a version that fails this. Mode bits only detect: the owner can set them back, so the hash is what catches a change; the mode check is the early warning.
- These are structural refusals, not health checks.
- A seat runs from no copy of its own. marvel renders the prompt from the pinned version at launch, passes it to the harness, and stamps its hash on the session (the existing `marvel.prompt_artifact.sha256`). The seat's working directory is its `workdir`, never the store. So no seat can edit what it runs from, and nothing needs a per-seat copy.

## Verbs marvel owns

| verb | does |
|---|---|
| `marvel get companies` | each `name@version` in every root: hash, source, whether it is `current`, which teams use it |
| `marvel describe company <name[@version]>` | its definitions and their status |
| `marvel get cast` (#946) | the running teams, each with `name@version` and hash as resolved at launch |
| `marvel work` / `apply` | refuses a missing pin, a hash mismatch, a writable version or a retired definition |
| `marvel doctor` | reports root problems, read-only |
| `marvel config companies add <path>` / `remove <path>` | adds or removes a company root in marvel's own config, so a root is set by command rather than by editing `~/.marvel` by hand |
| `marvel company export builtin <dir>` | writes the builtin company out, to a path the user names; with the config verb, the only writes these verbs make |

marvel has no `install`, `remove`, `fetch` or `activate`. Anything that writes a valid version directory and, optionally, flips `current` is an installer: a package manager, `tar -x` of a release, or `git archive` by hand.

## What it replaces

Launch scripts that read a library at spawn and pass the slice in `--append-system-prompt`. With a company pinned, the claude adapter passes the rendered definition as its one system prompt (the same single-prompt rule as today, claude.go). Wrapper commands stay supported for anything else they do.

## Tests (for the builder's red/green PR)

- Given a role with neither `company` nor `definition`, when it launches, then its command and args are today's, and no root is read.
- Given a role with a `definition` and a team with no `company`, when it launches, then the prompt is the builtin's definition, and no root is read.
- Given `company = "x@1"` in no root, when `marvel work` runs, then it refuses and names the roots it read.
- Given `x@1` recorded with hash H and its tree later changed, when a role launches from it, then the launch is refused and names both hashes.
- Given `x@1` whose files are read-only but one directory is writable, when apply runs, then it refuses and names the directory.
- Given a root holding `x/.1.partial/` and `x/.lock`, when `marvel get companies` runs, then neither is listed or hashed.
- Given a root holding a name whose versions have no `company.toml`, then that name is not listed.
- Given `company = "x"` and `current` flipped from 1 to 2 while a session runs, then that session's record still shows `x@1`, and the next launch shows `x@2`.
- Given a definition with `status = "retired"`, when a role names it, then apply refuses.
- Given any of the above, the daemon writes nothing under any root (sandbox profile denying writes outside scratch, with a blocked-write control).

## Open for the ruling

- Name-only pins as the default for fleet teams, with exact pins for experiments and rollback. I recommend this; it is what removes the per-host edit.
- Company roots from marvel's config only, with no default root. A user who installs with sideshow adds sideshow's store to `companies.roots` (sideshow `docs/marvel-company-target.md`).

This recommendation is valid until 2026-11-08; the architect re-checks it then.
