# Companies: installed definition libraries, and the cast pinned to one

Status: design, for a ruling. Revised 2026-11-20, after the first recommendation lapsed on 2026-11-08 and was sent back for revision. The readers are built and nothing calls them yet: `internal/company` decodes `company.toml` and recipe files, resolves a pin, runs the launch checks and computes the tree hash. No manifest key, verb or launch path uses it. Sections "What changed since 2026-11-01" and "What is now understood" are new; the rest is the 2026-11-01 design with the changes those sections name.

## Why

A team's roles today get their prompts from whatever a launch script reads at spawn. marvel cannot say which library a running team came from, at which version, or whether two teams on one cluster run the same text. Rolling a library forward or back means editing files on each host, and any seat or person can change those files in place. This design gives marvel a native object for the library, the **company**. Each team pins one version. Only a tool writes the files a cast is drawn from, and seats get a working copy with a digest back to its source. marvel reads, pins and records, and installs nothing from outside, so it still works with nothing but its own binary.

Vocabulary (ruled): a **company** is a library of team, workflow and agent definitions. The **cast** is the set of teams running on one cluster, each drawn from a company. "Pack" is not a marvel word.

## What changed since 2026-11-01

Read at marvel main 41c8dff1.

| change | where | what it means here |
|---|---|---|
| The company and recipe schema | `docs/design/company-recipe-schema.md` (#1346) | The keys of `company.toml`, the two library kinds (`definitions`, `recipes`), the recipe format and the refusal table are written down. This design points at it and does not restate it. |
| Strict decoders | `internal/company/company.go`, `recipe.go` (#1351) | `DecodeCompany` and `DecodeRecipe` read the schema and refuse what it refuses. |
| The pin resolver and the launch checks | `internal/company/resolve.go`, `check.go` (#1359) | `Resolve` turns a pin into a library through an `Env` the caller supplies; `Check` runs the per-launch checks (definition present, status, recipe fit). Nothing calls either yet (`internal/company/doc.go`). |
| The tree hash | `internal/company/treehash.go`, with the worked vector below as a test | The v1 definition below is code, and an installer can be checked against it. |
| A per-role sandbox profile | `sandbox_profile` on a role (`internal/api/manifest.go`), launch under `sandbox-exec` (#1260); example profiles for opencode (#1494, #1497) | E1 no longer needs a new launch path. marvel already launches a role under a profile; E1 becomes the deny rules for company roots that marvel adds to the profile it applies. |
| Teams mix harnesses | claude, codex, opencode and goose seats run in one team today | A recipe per role, pinned beside the company, is how a team says which harness and model each seat uses. |

## What is now understood

- **A library may carry more than marvel reads.** `Check` reads `company.toml` and `roles/<id>.md` relative to the version folder, and checks that each `templates/<name>.txt` a recipe's prompt names is a regular file there (refusal N6, `internal/company/check.go`); it lists nothing else, so a release folder can hold other content beside them (for example the source tree and the scripts its own launcher uses). marvel ignores what it does not name. A library can therefore ship one folder, one version and one tree hash for every reader, instead of a separate marvel-only package.
- **A package store is already a valid root.** A store laid out `<store>/<name>/<version>/` with a `current` link, staging entries starting with `.`, and other content beside it, fits "Stores and roots" as written. Adding such a store with `marvel config companies add` is the whole integration. The installer needs no marvel code, and marvel needs no installer code.
- **A company can be generated.** A library built from someone else's release (a generator plus a pinned upstream version, writing one definition per seat) is still a company: `company.toml` lists its definitions and records the upstream in `source`. marvel neither knows nor checks how it was built, only the hash of what is installed.
- **The per-team pin is what lets two versions run side by side.** Two teams on one cluster can pin two versions of the same library, and moving one never moves the other. Nothing global changes on an upgrade.
- **Knowing which company a team runs is the blocker for the cast view.** `marvel get cast` (#946) cannot be built until a team records the `name@version` it launched from, which is the first wiring step below.

## Three tiers

| tier | what | written by | never written by |
|---|---|---|---|
| **master** | the library's git repository, at a commit | its owner, through that repository's review | marvel, sideshow, any seat at run time |
| **reference copy** | one installed version, `<root>/<name>/<version>/` | the tool that owns the store it sits in: sideshow for its store, marvel for its own (below) | seats, launchers, people, `marvel cp` |
| **working copy** | what one session runs from | marvel, at launch, into that session's directory | any installer; it is never promoted |

A reference copy is installed from the master at a version. A working copy is derived from a reference copy. Nothing flows back up: a working copy is never installed as a reference copy, and a reference copy is never edited into a new version.

## Stores and roots

- marvel reads reference copies from **company roots**, each laid out `<root>/<name>/<version>/`.
- **marvel's own store** is `~/.local/share/marvel/companies`. marvel writes only one thing there: the **builtin** company, which it embeds in the binary as it already embeds `marvel example`. At daemon start it materializes the builtin as `builtin/<marvel version>/` if that version is absent, by the same staged, read-only, no-replace steps an installer uses. That is not an install of outside content: the bytes come from marvel's own binary. The name `builtin` is reserved in every other root.
- **Other roots** come from the config key `companies.roots`, set with `marvel config companies add|remove <path>` (never by hand-editing `~/.marvel`). A root belongs to the tool that writes it. The sideshow store is the expected one.
- Roots are read from config, never from the environment, so a daemon started by launchd or systemd resolves the same paths as a shell. marvel reads the key **at each apply and each launch**, so an added or removed root applies to the next one, with no restart. A running session is not affected.
- **What marvel skips:** any entry whose name starts with `.` (an installer's staging and lock files), any `<version>` without a `company.toml`, and any `<name>` with no such version. A root can therefore be a general package store holding other content.
- **Ambiguity:** a name held in two roots is refused, for an exact pin and for a name-only pin alike, naming both roots. marvel never picks one.
- marvel refuses a root that is inside, or contains, a `marvel cp` jail (`docs/design/marvel-cp.md`), so the file channel can never write a reference copy. The same set of paths, read the same way, is the one `marvel cp` refuses.

### Layout of a version

```
<name>/<version>/
  company.toml        name, version, kind, schema, requires_marvel, source (the master's URL and commit), definitions index
  roles/<id>.md       role definitions
  identities/<id>.md  identity definitions (optional)
  templates/cast.txt  the sentence that opens a prompt (optional)
  teams/<id>.toml     team templates (optional)
  workflows/<id>.md   workflow definitions (optional)
  recipes/<name>.toml launch recipes (a library of kind "recipes" holds these and no definitions)
```

The keys of `company.toml`, the recipe file format, the two library kinds and the refusal messages are in [company-recipe-schema.md](company-recipe-schema.md).

`company.toml` lists each definition with a `status`: `ratified`, `proposal` or `retired`. marvel refuses to launch a `retired` definition, and launches a `proposal` only when the role sets `allow_proposal = true`.

## How write ownership is enforced

Convention is not enough, because in a single-user fleet every seat runs as the same user as the tools. So:

- **E1, at launch (marvel, every host).** marvel starts each session under a write-deny profile covering every company root and every working copy but the session's own (macOS `sandbox-exec`; Linux Landlock). A seat therefore cannot write a reference copy, whatever its mode bits say. The profile denies only those paths; everything else the harness does is unchanged. A refused write is the sandbox's own error, and marvel records the profile it applied on the session.
- **E2, by ownership (opt-in, per host).** For people and tools that marvel does not launch, a host owner can make a store belong to a dedicated user, writable only through that user (the owning tool run with `sudo -u`). This needs elevated rights once per host, which is the point, as for `marvel cp`'s policy file. marvel checks nothing about E2; it is the owner's.
- **E0, detection (always).** Every reference copy is read-only (no write bit on any file or directory), and marvel records its tree hash on first sight. Mode bits and the hash detect a change; they do not prevent one. A version that fails either is refused.

## The tree hash (v1)

- **Entries:** every file, directory and symbolic link under `<version>/`. The version directory itself (`.`) is not an entry. Nothing is followed: a link is recorded as a link.
- **Path:** relative to `<version>/`, components joined by `/`, as the raw bytes on disk. A path may hold any byte but NUL and `/`, a newline included.
- **Record:** `<type> NUL <path> NUL <value> NUL`, where type is `f`, `d` or `l`; value is the lowercase hex sha256 of a file's bytes, empty for a directory, and a link's target bytes as `readlink` returns them.
- **Order:** records sorted by path bytes. **Hash:** lowercase hex sha256 of the records concatenated. Mode bits, owners and times are not hashed; read-only is checked separately (E0).

Worked vector. A version holding `company.toml` with bytes `name = "x"\n`, a directory `roles`, an empty file `roles/a\nb.md` (a newline in its name), a file `roles/x.md` with bytes `hi\n`, and a link `top` pointing at `roles/x.md`:

```
f\0company.toml\03eb10e9d710d38dc9fe5566f14f7d2cb97ca68a5c6c6b00150b73c69064410cc\0
d\0roles\0\0
f\0roles/a<LF>b.md\0e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\0
f\0roles/x.md\098ea6e4f216f2fb4b69fff9b3a44842c38686ca685f3f55dc48c5d3fb1107be4\0
l\0top\0roles/x.md\0
```

264 bytes, tree hash `d44bd0c103e09a4e353971211d892ffcd9c5aad767eeff24a258211f7e0f5dcf`. (`<LF>` is the single byte 0x0a; the line breaks above are for reading and are not in the input.) An installer that records a hash uses this definition, so its record and marvel's can be compared.

## Working copies

- At launch marvel writes the session's working copy under its own state, `~/.marvel/run/sessions/<session>/company/`. It holds the rendered definition the harness is given and `origin.json`: `name@version`, the reference copy's tree hash, and the sha256 of each file it wrote.
- The rendered prompt's hash is stamped on the session as today (`marvel.prompt_artifact.sha256`). The seat's working directory is its `workdir`, never a root and never the working copy.
- A seat may read its working copy. E1 lets it write only its own, and marvel reports a working copy whose files no longer match `origin.json` in `marvel describe session`. A change made there does not change what the running harness was given.
- A change worth keeping goes to the master by its owner's review, and comes back as a new reference copy. marvel refuses a root that contains an `origin.json` tree, and a sideshow source that holds one is refused there too, so a working copy cannot be installed by pointing a tool at it.
- A working copy is removed with its session's state.

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

- **Exact pin** (`name@version`): reproducible. Apply refuses a pin that is in no root.
- **A second pin** (`recipes = "name"` or `"name@version"`) names a library of kind `recipes`, checked on its own and then against the `company` pin (company-recipe-schema.md).
- **Name only:** the team follows that name's `current`, so a library rolls forward with no edit at the host: the owning tool flips `current`, and the next launch or shift picks it up.
- **How `current` is read:** a company name is a real directory under its root, each version a real directory inside it that holds a `company.toml`, and `current` is a symbolic link beside them. marvel reads the link once, as a single path element that names a listed version, and then reads everything through that version's directory. Entries whose names start with `.`, the name `current` itself, symbolic links standing in for a name or a version, and directories with no `company.toml` are not listed. A link that is missing, is not a link, points outside the company, or names a version that is not listed refuses the launch, naming the team key and the pin, and marvel does not fall back to another version.
- Either way, the version is **resolved once per launch** and recorded on the session. A running session never changes version.
- A role with no `definition` behaves as today. Nothing existing changes until a manifest opts in.

## Verbs marvel owns

| verb | does |
|---|---|
| `marvel get companies` | each `name@version` in every root: root, hash, source, whether it is `current`, which teams use it |
| `marvel describe company <name[@version]>` | its definitions and their status |
| `marvel get cast` (#946) | the running teams, each with `name@version` and hash as resolved at launch |
| `marvel describe session <s>` | adds the working copy's origin and whether it still matches |
| `marvel work` / `apply` | refuses a missing pin, an ambiguous name, a hash mismatch, a writable version or a retired definition |
| `marvel doctor` | reports root and working-copy problems, read-only |
| `marvel config companies add|remove <path>` | sets the roots by command |
| `marvel company export builtin <dir>` | writes the builtin out to a path the user names, for forking into a new master |

marvel writes reference copies only in its own store, and only the builtin. It has no `install`, `fetch` or `activate` for outside content: another library comes from an installer that owns its own store (sideshow `docs/marvel-company-target.md`). Unpacking a tree into a root by hand is not a supported install; such a tree has no owning tool, and on a host using E2 the owner cannot write the store anyway.

## Build order

Each step is its own PR, red test first, and lands before the next one starts. The tests are in the next-but-one section.

1. **Wire the pins.** `company` and `recipes` keys on a team in the manifest; at launch, `Resolve` and `Check`; the resolved `name@version` and tree hash recorded on the session. A role with neither key launches as today.
2. **Roots by command.** `companies.roots` in the client config, `marvel config companies add|remove`, read at each apply and launch.
3. **The views.** `marvel get companies`, `marvel describe company`, and `marvel get cast` (#946).
4. **The builtin.** Embed and materialize `builtin/<marvel version>/`, and `marvel company export builtin`.
5. **E1.** Add deny-write rules for every root and every other session's working copy to the profile a session launches under.
6. **Working copies.** `origin.json` and the mismatch report in `describe session` and `doctor`.

Steps 1 to 3 give a team a pinned library and a view of it. Steps 4 to 6 can follow in any order.

## What it replaces

Launch scripts that read a library at spawn and pass the slice in `--append-system-prompt`. With a company pinned, the claude adapter passes the rendered definition as its one system prompt (the same single-prompt rule as today, claude.go). Wrapper commands stay supported for anything else they do.

## Tests (for the builder's red/green PR)

- Given a role with neither `company` nor `definition`, when it launches, then its command and args are today's, and no root is read.
- Given a role with a `definition` and a team with no `company`, when it launches, then the prompt is the builtin's definition.
- Given a fresh marvel store, when the daemon starts, then `builtin/<marvel version>/` exists, read-only, with the hash of the embedded bytes; a second start writes nothing.
- Given `company = "x@1"` in no root, when `marvel work` runs, then it refuses and names the roots it read.
- Given `x` in two roots, when a team pins `x` or `x@1`, then apply refuses and names both roots.
- Given a root added with `marvel config companies add` while the daemon runs, then the next apply sees it, with no restart.
- Given `x@1` recorded with hash H and its tree later changed, when a role launches from it, then the launch is refused and names both hashes.
- Given `x@1` whose files are read-only but one directory is writable, then apply refuses and names the directory.
- Given a root holding `x/.1.partial/` and `x/.lock`, then neither is listed or hashed. Given a name whose versions have no `company.toml`, then it is not listed.
- Given the worked vector's tree, then the computed hash equals the one above.
- Given a session launched under E1, when it writes into a root, then the write fails with the sandbox's own error, and a write into its own working copy succeeds.
- Given a working copy edited after launch, then `describe session` reports the mismatch, and a root or sideshow source containing it is refused.
- Given `company = "x"` and `current` flipped from 1 to 2 while a session runs, then that session's record still shows `x@1`, and the next launch shows `x@2`.
- Given a definition with `status = "retired"`, then apply refuses.
- Given any of the above, the daemon writes nothing under any root but its own store (sandbox profile denying writes outside scratch, with a blocked-write control).

## Open for the ruling

1. Name-only pins as the default for fleet teams, with exact pins for experiments and rollback. I recommend this; it is what removes the per-host edit.
2. E1 on by default for every session marvel launches, once step 5 lands. I recommend it: without it, write ownership is convention. Since #1260 it is a set of rules added to the launch profile, not a new launch path.
3. E2 per host, the owner's choice.
4. The build order above, starting with step 1. I recommend it: step 1 is the smallest change that makes a team's library visible, and #946 waits on it.

This recommendation is valid until 2026-12-04, or until a manifest key for `company` or `recipes` lands, whichever comes first. The architect re-checks it then.
