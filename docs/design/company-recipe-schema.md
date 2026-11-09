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
recipes = "acme-recipes@2026.11.01-ecc28ac"   # exact pin

[[teams.roles]]
name = "builder"
definition = "role/builder"
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
| `version` | both | yes | equals its directory name |
| `kind` | both | yes | `"definitions"` or `"recipes"` |
| `schema` | both | yes | the layout version of this file; `1` |
| `requires_marvel` | both | yes | the oldest marvel that reads this library, as a version |
| `source` | both | yes | `{ url, commit }` of the master it was built from |
| `definition_schema` | definitions | yes | the shape of role files and the index below; `1` |
| `recipe_schema` | recipes | yes | the shape of recipe files; `1` |
| `requires` | recipes | yes | `{ definition_schema = N }`, the definitions shape the recipes expect |
| `[[definitions]]` | definitions | yes | the index: `id`, `status`, `sha256` |

`status` is `ratified`, `proposal` or `retired` (companies.md). `sha256` is the lowercase hex hash of the finished role file. A `recipes` library carries no `[[definitions]]`, and a `definitions` library carries no `recipe_schema` or `requires`; either is an unknown key for its kind.

This supersedes the earlier example that put `recipe_schema = 1` in the definitions library's `company.toml`. A definitions library names `definition_schema`; only a recipes library names `recipe_schema`.

```toml
# a definitions library
name = "acme-roles"
version = "2026.11.01-ecc28ac"
kind = "definitions"
schema = 1
requires_marvel = "0.9.0"
definition_schema = 1
source = { url = "https://example.invalid/acme-roles.git", commit = "ecc28ac" }

[[definitions]]
id = "role/builder"
status = "ratified"
sha256 = "<64 hex digits>"
```

```toml
# a recipes library
name = "acme-recipes"
version = "2026.11.01-b41d0e2"
kind = "recipes"
schema = 1
requires_marvel = "0.9.0"
recipe_schema = 1
requires = { definition_schema = 1 }
source = { url = "https://example.invalid/acme-recipes.git", commit = "b41d0e2" }
```

### Checks at launch, in order

1. Find each pinned name in the roots. Absent is refused; present in two roots is refused, naming both (companies.md).
2. Resolve the version.
3. E0: read-only files and a matching tree hash (companies.md).
4. `requires_marvel`: refused when this marvel is older.
5. Each library's own schema: marvel carries the list of `definition_schema` and `recipe_schema` values it reads, and refuses a library whose value is not on its list.
6. The pair: the recipes library's `requires.definition_schema` must equal the definitions library's `definition_schema`.
7. Read the definition and the recipe. Refuse a `retired` definition, and a `proposal` unless the role sets `allow_proposal`.

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

`unset` lists environment names to remove from the seat's environment. A name set by two blocks that apply to one launch is refused at read, so there is no precedence to remember. `when` blocks apply in name order, and the base `[env]` and `args` come first.

### Pre-flight

The command runs under the session's environment before the harness starts, in its own process group. A nonzero exit fails the launch, and the failure carries the command's output. On timeout marvel kills the process group and fails the launch, naming the command and the elapsed time.

`timeout` is optional. marvel holds a default for when it is absent and a ceiling that a recipe may not exceed (a larger value is refused at read). The two numbers are not set here: they are fields the build fills from a measurement of the real pre-flight on each host, so none is guessed in a design.

## Refusal messages

Fixed text, so the tests and the admin guide quote one wording. Angle brackets are filled in; nothing here echoes a parameter value.

| case | message |
|---|---|
| unknown key | `company <name>@<version>: <file>: unknown key "<key>"` |
| a pin names a removed or absent version | `team <team>: <key> pin <name>@<version> is not installed in any root; marvel does not fall back to current` |
| a library's schema is not one marvel reads | `company <name>@<version>: <definition_schema or recipe_schema> = <n> is not read by this marvel (reads <list>)` |
| the pair disagrees | `recipes <name>@<version> requires definition_schema = <n>, but company <name>@<version> has definition_schema = <m>` |
| marvel too old | `company <name>@<version> requires marvel <v> or newer; this is <w>` |
| pattern failed | `team <team> role <role>: parameter <name> does not match its pattern` |
| tool not set | `recipe <name>: tool "<tool>" is not set; run marvel config tools set <tool> <path>` |
| pre-flight failed | `pre-flight <command> exited <code>: <output>` |
| pre-flight timed out | `pre-flight <command> exceeded <timeout> (elapsed <duration>); process group killed` |

A pin to a version sideshow removed fails the next restart with the second message and names the team key and, where marvel read the pin from a manifest file, that file and line. It never starts the seat on `current` in its place. Rolling back one team means pinning the previous exact version and shifting.

## Independence

marvel renders whatever tools a recipe names and knows nothing about the projects that write the libraries. With no company pinned, marvel runs as it does today. Recipes are optional data, and a recipe library can be written by anyone who follows this schema.

## Not in this document

- The code, and the tests for it; they belong to the build.
- The two pre-flight numbers (above).
- The spawn log line and its `prompt=` hash.
- Which files a definitions library builds from its sources; that stays with its owner.
