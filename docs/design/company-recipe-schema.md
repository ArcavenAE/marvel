# Company and recipe schema

Status: design, for review. Nothing here is built. These are spec choices, and review can still challenge them.

## Why

A seat's role text and launch settings can live in installed, versioned libraries that marvel reads, so a launch script stops being the place those decisions hide. Two other projects write those libraries (one holds role definitions, one holds launch recipes), and a third installs them. Each has to write files marvel will accept, so the file formats and the refusal rules need one written home before any of them starts. This document is that home. It adds no marvel behavior; it fixes the shapes that [companies.md](companies.md) leaves open.

Not decided here: what the spawn log line's `prompt=` hash covers. The recommended default is the role file alone, with the whole prompt recorded in the session's own system-prompt hash. That default is pending the operator and this document does not settle it.

## Two kinds of library

A company is an installed library at `<root>/<name>/<version>/` with a `company.toml` (companies.md). Two kinds exist, and a team pins one of each:

| kind | holds | pinned by |
|---|---|---|
| `definitions` | `roles/<id>.md`, `identities/<id>.md`, `templates/cast.txt` | the team key `company` |
| `recipes` | `recipes/<name>.toml` | the team key `recipes` |

```toml
[[teams]]
name = "core"
company = "acme-roles"            # follows that name's current
recipes = "acme-recipes@2026-11-01-b41d0e2"   # exact pin

[[teams.roles]]
name = "builder"
definition = "role/builder--steady"
recipe = "bus-seat"
workdir = "/srv/project"
params = { scope = "marvel", notes = "ops/seat-builder.md" }
```

Each pin is a name, or `name@version`. A name-only pin follows the name's `current`; an exact pin holds. Both resolve at every launch, restarts included, and the resolved `name@version` is recorded on the session. A team that must never run mixed versions pins exact versions.

Rejected: one `libraries = [...]` list. A list leaves the kind of each entry to be read out of the files, so an error could not name the key at fault, and the pair check below would have to infer which entry is which. Two named keys let apply say `company` or `recipes` and check each pin on its own.

A role with no `definition` launches as it does today, and a role with a `definition` and no `recipe` uses the adapter's own launch with the definition as its one system prompt.

## company.toml

Strict: an unknown key anywhere in the file is refused, naming the company, the file and the key. marvel's goose table decoder is the precedent (an unknown key is an error, not a dropped line).

| key | kind | required | meaning |
|---|---|---|---|
| `name` | both | yes | the library name; equals its directory name |
| `version` | both | yes | `<commit date>-<short sha>`, the form the installer names the version directory with; equals the directory name |
| `kind` | both | yes | `"definitions"` or `"recipes"` |
| `schema` | both | yes | the layout version of this file; `1` |
| `requires_marvel` | both | yes | the oldest marvel that reads this library, written as that marvel's full version string (an alpha's is `0.1.0-alpha.<yyyymmdd>.<hhmmss>.<sha7>`, ordered by the date and time in it, and a prerelease is below its release) |
| `source` | both | yes | the master it was built from. A `recipes` library: `{ url, commit }`, stamped by the library's own build step. A `definitions` library: `{ url, contents_tree }` and an optional `commit` |
| `definition_schema` | definitions | yes | the shape of role files and the index below; `1` |
| `recipe_schema` | recipes | yes | the shape of recipe files; `1` |
| `requires` | recipes | yes | `{ definition_schema = N }`, the definitions shape the recipes expect |
| `[[definitions]]` | definitions | yes | the index; the entry keys are below |

A `recipes` library stamps its `version` and `source` when it is built, so a recipes library has one rule with a definitions library and a launch can name the commit it came from. The stamp step belongs to that library's own build, not to marvel.

A `recipes` library carries no `[[definitions]]`, and a `definitions` library carries no `recipe_schema` or `requires`; either is an unknown key for its kind.

This supersedes the earlier example that put `recipe_schema = 1` in the definitions library's `company.toml`. A definitions library names `definition_schema`; only a recipes library names `recipe_schema`.

### Definition entries

An entry is a role on its own, or a role finished together with an identity (a pair). Each entry names the finished file a seat loads.

| key | on | meaning |
|---|---|---|
| `id` | all | `role/<role>` for a role on its own, `role/<role>--<identity>` for a pair; a manifest's `definition` names an entry by this `id` alone. `--` cannot occur inside a role or identity name |
| `version` | all | the role's own version |
| `identity` | pairs only | `<identity>@<version>`; absent on a role on its own |
| `status` | all | `ratified`, `proposal` or `retired` (companies.md) |
| `identity_status` | pairs only | the identity's status, with the same three values |
| `sha256` | all | the lowercase hex hash of the finished file |
| `slice`, `border` | all | `sha256:<hex>` values the library copies from its own index; marvel records them and never recomputes either |

The finished file of an entry is `roles/<id without "role/">.md`, so a pair is `roles/<role>--<identity>.md`. A pair's file already holds the identity text, so marvel refuses a recipe whose `prompt` lists `identity` when the entry carries an `identity` (the identity body must appear once).

An entry carries `identity` if and only if its `id` contains `--`, and the part of the `id` after `--` equals the name part of `identity` (the text before `@`). marvel checks this when it reads the index and refuses the whole library when one entry disagrees, so a library cannot name one identity in the id and ship another in the file.

marvel refuses a `proposal` in either `status` or `identity_status` unless the role sets `allow_proposal`, and refuses a pair whose `identity_status` is `retired`, as it refuses a `retired` `status`. The pair grammar was chosen over keeping `id = "role/<role>"` and adding a `path` key; the panel that weighed it split 3 to 2, and a review can reopen it.

```toml
# a definitions library
name = "acme-roles"
version = "2026-11-01-ecc28ac"
kind = "definitions"
schema = 1
requires_marvel = "0.1.0-alpha.20261008.044910.0d33f29"
definition_schema = 1
source = { url = "https://example.invalid/acme-roles.git", contents_tree = "<40 hex digits>", commit = "ecc28ac" }

[[definitions]]
id = "role/builder--steady"
version = "1.2.0"
identity = "steady@1.0.0"
status = "ratified"
identity_status = "ratified"
sha256 = "<64 hex digits>"
slice = "sha256:<64 hex digits>"
border = "sha256:<64 hex digits>"
```

```toml
# a recipes library
name = "acme-recipes"
version = "2026-11-01-b41d0e2"
kind = "recipes"
schema = 1
requires_marvel = "0.1.0-alpha.20261008.044910.0d33f29"
recipe_schema = 1
requires = { definition_schema = 1 }
source = { url = "https://example.invalid/acme-recipes.git", commit = "b41d0e2" }
```

### Checks at launch, in order

1. Find each pinned name in the roots. Absent is refused; present in two roots is refused, naming both (companies.md).
2. Resolve the version.
3. E0: read-only files and a matching tree hash (companies.md).
4. `kind`: the `company` pin must name a `definitions` library and the `recipes` pin a `recipes` library.
5. The index: each entry's `identity` agrees with its `id` (see Definition entries).
6. `requires_marvel`: refused when this marvel is older. A marvel with no orderable version, a build that reads `dev`, passes this check unchecked and the result says so (`unchecked: dev`); the schema check in step 7 still applies.
7. Each library's own schema: marvel carries the list of `definition_schema` and `recipe_schema` values it reads, and refuses a library whose value is not on its list, and a recipe file whose `schema` is not its library's `recipe_schema`.
8. The pair: the recipes library's `requires.definition_schema` must equal the definitions library's `definition_schema`.
9. Read the definition and the recipe. Refuse a `retired` definition, a pair whose `identity_status` is `retired`, a `proposal` in `status` or `identity_status` unless the role sets `allow_proposal`, and a recipe whose `prompt` lists `identity` against an entry that carries one.
10. Merge the environment (below), refusing a key that the recipe and the role both name.

A pin that is absent fails at step 1 with no fallback.

## Recipe file

`recipes/<name>.toml`, one launch shape each. Closed data: no logic beyond the `when` blocks below. Strict decoding as above, in every table.

```toml
schema = 1
adapter = "claude"
command = "tool:seat-start"
prompt = ["template:cast", "definition", "notes"]

[params.cue]
type = "bool"

[params.scope]
type = "string"
required = true
pattern = "^[a-z][a-z0-9-]{0,63}$"

[env]
BUS_ROLE = "{role}"

[mcp.bus]
command = "tool:bus-shim"
strict = true

[preflight]
command = ["tool:bus-shim", "--preflight"]
timeout = "20s"

[when.cue]
env = { BUS_CUE = "1" }
args = ["--load-channel", "server:bus"]

[when.global.holder]
env = { BUS_GLOBAL_DOMAIN = "{var:bus.global_domain}" }

[when.global.non_holder]
unset = ["BUS_GLOBAL_DOMAIN", "BUS_GLOBAL_ROLE"]
```

The names in the example are placeholders for the shape; a recipe names whatever tools and variables its owner has.

| key | meaning |
|---|---|
| `schema` | the recipe file's own version; must equal the library's `recipe_schema` |
| `adapter` | a registered adapter name; unknown is refused at apply |
| `command` | the harness command, or a `tool:` name; absent means the adapter's own |
| `prompt` | the order of the one system prompt: `template:<name>`, `definition`, `identity`, `notes` |
| `args` | extra command-line arguments, in order |
| `[env]` | environment entries, name to value |
| `[mcp.<name>]` | one MCP server: `command`, `args`, `strict`; marvel renders `--mcp-config`, and `strict = true` adds `--strict-mcp-config` |
| `[preflight]` | a command run before the harness, and its `timeout` |
| `[params.<name>]` | a declared role parameter: `type` (`string` or `bool`), `required`, `pattern` |
| `[when.<param>]` | applies when that bool parameter is true: `env`, `args`, `unset` |
| `[when.global.holder]`, `[when.global.non_holder]` | applies by whether the role holds the global tier, as marvel's existing resolver says; the recipe does not decide who is a holder |

### Values

A string value may carry a closed set of placeholders, and nothing else is expanded:

- `{role}`, `{team}`, `{workspace}`, `{session}`: the launch's own names.
- `{param:<name>}`: a declared parameter, from the role's `params`.
- `{var:<name>}`: a host value, set with `marvel config vars set` (new, not built).
- `tool:<name>`: valid only as a whole `command` value or the first element of a command list. It resolves from marvel's config (`marvel config tools set <name> <path>`, new, not built), so a recipe holds no host path. Every configured tool path joins the write-deny set a seat runs under (companies.md, E1).

An unknown placeholder is refused when the recipe is read, naming the key. A parameter used but not declared is refused the same way. Declared parameters with a `pattern` are checked at launch, and a value that fails is refused naming the field, never echoing the value.

`unset` lists environment names to remove from the seat's environment. `when` blocks apply in name order, and the base `[env]` and `args` come first.

Which blocks can apply to one launch together: the base with any `when.<param>` block, two `when.<param>` blocks, and a `when.global.<branch>` block with the base or with a `when.<param>` block. `when.global.holder` and `when.global.non_holder` never apply together, because a role is one or the other. If two blocks that can apply together name the same key, in `env` or in `unset`, the recipe is refused at read, naming both blocks and the key. `unset` counts as naming a key, so an unset beside a set of that key is a conflict, and so are two unsets of it. The check uses the recipe alone, with no parameter values, so two blocks that might apply together are refused even when no role sets both. There is no precedence to remember.

The passing case is a `when.global.holder` block that sets `BUS_GLOBAL_DOMAIN` and a `when.global.non_holder` block that unsets it: they never apply to one launch, so they do not conflict.

A recipe and a role never compete for a key. If a recipe's `env` or `unset` (a base entry or an applying `when` block) and the role's `runtime.env` name the same key, the launch is refused, naming the role, the recipe and the key. Neither side wins and there is no order to learn. This covers the case of a role that is not a global holder setting a key the recipe unsets for it.

### Pre-flight

The command runs under the session's environment before the harness starts, in its own process group. A nonzero exit fails the launch, and the failure carries the command's output. On timeout marvel kills the process group and fails the launch, naming the command and the elapsed time.

`timeout` is optional. marvel holds a default for when it is absent and a ceiling that a recipe may not exceed (a larger value is refused at read). The two numbers are not set here: they are fields the build fills from a measurement of the real pre-flight on each host, so none is guessed in a design.

## Refusal messages

Fixed text, so the tests and the admin guide quote one wording. Angle brackets are filled in; nothing here echoes a parameter value.

| case | message |
|---|---|
| unknown key | `company <name>@<version>: <file>: unknown key "<key>"` |
| a pin names a removed or absent version | `team <team>: <key> pin <name>@<version> is not installed in any root; marvel does not fall back to current (roots read: <roots>)` |
| a library's schema is not one marvel reads | `company <name>@<version>: <definition_schema or recipe_schema> = <n> is not read by this marvel (reads <list>)` |
| the pair disagrees | `recipes <name>@<version> requires definition_schema = <n>, but company <name>@<version> has definition_schema = <m>` |
| marvel too old | `company <name>@<version> requires marvel <v> or newer; this is <w>` |
| pattern failed | `team <team> role <role>: parameter <name> does not match its pattern` |
| a required parameter is not set | `team <team> role <role>: required parameter <name> is not set` |
| tool not set | `recipe <name>: tool "<tool>" is not set; run marvel config tools set <tool> <path>` |
| identity listed twice | `recipe <name>: prompt lists identity, but definition <id> already carries identity <identity>` |
| env named twice | `team <team> role <role>: recipe <name> and runtime.env both name <KEY>` |
| a retired definition | `team <team> role <role>: definition <id> is retired` |
| a retired identity | `team <team> role <role>: definition <id> has identity <identity> marked retired` |
| identity and id disagree | `company <name>@<version>: <file>: definition <id>: identity <identity> does not match the id` |
| a name in two roots | `company <name> is in two roots: <root> and <root>` |
| a pin names the wrong kind | `team <team>: <key> pin <name>@<version> is a library of kind <kind>, but <key> needs kind <kind>` |
| an unknown placeholder | `recipe <name>: <key>: unknown placeholder "<text>"` |
| an undeclared parameter | `recipe <name>: <key>: parameter "<name>" is not declared` |
| blocks name one key | `recipe <name>: blocks <block> and <block> both name <KEY>` |
| an unknown adapter | `recipe <name>: adapter "<adapter>" is not registered` |
| the recipe's schema differs | `recipe <name>: schema = <n>, but the library's recipe_schema is <m>` |
| timeout over the ceiling | `recipe <name>: preflight timeout <timeout> is over the ceiling <ceiling>` |
| a proposal without the flag | `team <team> role <role>: definition <id> is a proposal (<status or identity_status>) and the role does not set allow_proposal` |
| pre-flight failed | `pre-flight <command> exited <code>: <output>` |
| pre-flight timed out | `pre-flight <command> exceeded <timeout> (elapsed <duration>); process group killed` |
| a credential key in a recipe | `recipe <name>: <block>: key <KEY> is a credential and may not be named in a recipe` |
| a key marvel sets, in a recipe | `recipe <name>: <block>: key <KEY> is set by marvel and may not be named in a recipe` |
| a parameter named `global` | `recipe <name>: params.global: parameter name "global" is reserved` |
| `tool:` out of place | `recipe <name>: <key>: tool: is valid only as a whole command or the first element of a command list` |
| name or version differs from its directory | `company <name>@<version>: company.toml: <which> "<value>" does not equal its directory "<dir>"` |
| an index field is malformed | `company <name>@<version>: <file>: definition <id>: <field> is not well formed` |
| a required key is missing | `company <name>@<version>: <file>: required key "<key>" is missing` |
| an env or unset name is not a plain name | `company <name>@<version>: recipe <recipe>: <section> key <KEY> is not a plain name` |
| a company key of the wrong form | `company <name>@<version>: company.toml: <key> must be <form>` |
| a recipe value of the wrong form | `recipe <name>: <key>: must be <form>` |
| company.toml does not parse | `company <name>@<version>: company.toml does not parse (line <n>)` |
| a recipe does not parse | `recipe <name>: recipes/<name>.toml does not parse (line <n>)` |
| a name-only pin names a name in no root | `team <team>: <key> pin <name> is not installed in any root; marvel does not fall back (roots read: <roots>)` |
| `current` names a version that is not installed | `team <team>: <key> pin <name> follows current <version>, which is not installed in any root; marvel does not fall back` |
| `current` is missing or not a link | `team <team>: <key> pin <name> has no readable current in <root>` |
| `current` points outside its company | `company <name> in <root>: current points outside <name>` |
| a recipe lists a template the library lacks (draft) | `recipe <name>: prompt lists template:<template>, but company <name>@<version> has no template <template>` |
| a recipe names a var that is not set (draft) | `recipe <name>: <key>: var "<var>" is not set; run marvel config vars set <var> <value>` |
| a definition file differs from its index entry | `company <name>@<version>: <file>: sha256 does not match the index entry <id>` |

In the `roots read` suffix `<roots>` is each configured root in configuration order, joined with `, `, or `none` when no root is configured. The two rows marked draft (the template and var rows) carry wording that is open for review.

A pin to a version sideshow removed fails the next restart with the second message and names the team key and, where marvel read the pin from a manifest file, that file and line. It never starts the seat on `current` in its place. Rolling back one team means pinning the previous exact version and shifting.

## The spawn line's `tree=`

A launch from a library writes `tree=` as the short commit of the resolved version (the part after the date in `<commit date>-<short sha>`). What the `prompt=` hash covers stays open, as above.

## Independence

marvel renders whatever tools a recipe names and knows nothing about the projects that write the libraries. With no company pinned, marvel runs as it does today. Recipes are optional data, and a recipe library can be written by anyone who follows this schema.

## Not in this document

- The code, and the tests for it; they belong to the build.
- The two pre-flight numbers (above).
- The spawn log line and its `prompt=` hash.
- Which files a definitions library builds from its sources; that stays with its owner.
