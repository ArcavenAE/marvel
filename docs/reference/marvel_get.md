## marvel get

List resources (sessions, teams, workspaces, endpoints, policies, credentials, budgets, account-readings, account-logins, vitals)

### Synopsis

List resources (sessions, teams, workspaces, endpoints, policies, credentials, budgets, vitals).

get account-readings lists the stamped readings of each account window the daemon holds: the percent
used, the reset time in UTC, when it was observed and the session that reported it. They are kept
on disk and read back at start, so the list and the stale rows of get budgets survive a restart.
--account keeps the accounts whose key contains the text, and --since 1h keeps readings no older
than that. -o json prints the same list. It reads the history only and asks nothing of any
harness; nothing gates or acts on it.

get account-logins lists each login's expiry state as the daemon's login expiry service last read it:
the state (ok, expiring, lapsed or unreadable), the expiry in UTC, whole hours left, when it was last
read, and why a read failed and since when. It reads the daemon's memory only and asks nothing of any
harness; -o json prints the same document. Login expiry is off unless the cluster config sets
login_expiry.

get vitals shows the daemon's latest host vitals sample (swap, memory pressure, fseventsd RSS and
the largest process's RSS, orphan CPU), its time, each threshold with its state, the top processes by
RSS (command name only, never arguments), the orphans (processes that lost their marvel parent),
and a GAP line for each vital the design lists and the
sampler does not hold yet. It reads the daemon's memory only, and -o json prints the same record as a
document. It is diagnostic: nothing gates or acts on it. host vitals is off unless the cluster
config sets host_vitals.

AGE is the time since the session was created, as 45s, 9m, 2h9m or 3d4h; a - means
the row has no creation time.

The account columns (opt-in; the set acct names all six, and wide holds account, 7d,
7d-reset and read) each carry one fact: ACCOUNT is the display name display.account_names
gives the account, else the label the role declared, else a short key; 5H% and 7D% are
the percent used, with a trailing ? once the reading is stale (the number is kept);
5H-RESET and 7D-RESET are the time to reset as +2d3h or +47m, now once past; READ is
the age of the reading. A - means no reading. Sessions on one account repeat the same
cells; nothing is summed or averaged across rows. On a cluster-set read the ACCOUNT
key folds in the cluster, so its keys differ from a single-cluster read.

SERVICE (opt-in, --columns service) is a set: RUNTIME, BACKEND, CRED, HOME and LOGIN,
which say in words which account a session spends against: harness, backend,
credential source, the account home, then the login label the role or team declared
with account_label. The backend is the one the environment resolved to, written
backend!intent where the role declared another. The home is its last element, or the
whole path where another home on the same cluster shares that element.
The label is declared, never checked against the login. Every one of these cells
is diagnostic only: nothing gates or acts on it. They are the first columns cut on a narrow terminal when
they came from rest. KEY and CLUSTER are columns too: on a cluster-set read KEY leads
the table when no column list is given, and a list decides whether it prints.
For a session on the operator's own login the source reads ambient:api-key+oauth-token
when those variable names were set in the tmux environment at spawn (names only, never
values); plain ambient means none were seen or the session was spawned before it was
recorded, and ambient:? means tmux could not say.
Absence is not proof of no key. To list seats that inherited a key:
  marvel get sessions -o json | jq '.[] | select(.ambient_bearer // "" | test("^(api-key|auth-token|oauth-token)"))'

ACTIVE% is the share of the last 15 minutes of daemon ticks that found the seat
not quiet, under the role's activity_timeout or the cluster quiet window. It
says "not quiet", not busy: a seat in a long tool call can read low. It is a -
until the daemon has watched for the whole 15 minutes, and for a seat with no
activity channel. A seat that has not reported a context reading shows a - too, and is
left out of the --load mean. Besides a seat whose feed has not fired yet, two working
seats read - this way: a headless codex exec seat, whose stream carries spend and no
context reading; and a headless seat the daemon adopted across a restart, whose stream
marvel does not reopen, so it reads - until the seat is relaunched. A heartbeat-fed seat
keeps its last reading across a restart.

For a seat with a statusline feed, ACTIVE% is provisional: the share of the last 15
minutes the seat spent waiting on model responses (API time), read from the same
statusline totals as COST. Tool runs count as inactive, so a busy seat can read
low and a seat in a long build near 0%. The definition is still being developed. It
is a - until 15 minutes of samples exist (including the first 15 minutes after the
total drops), and a ? once the feed has been silent past the window or went silent
for over three minutes inside it, as cost_rate does. Every other seat keeps the tick
reading above, and --load averages whatever each seat shows.

TOUT is output tokens, and RATE is output tokens a second. Both fill from the token
stream of a headless seat. An interactive claude seat fills TOUT from the output
tokens in its transcript (the main file only; subagent transcripts are not counted),
and RATE stays a -. An interactive codex seat fills TOUT from the output total in
its rollout, and RATE stays a -. A seat with neither shows a - in both. A trailing +
on TOUT marks a lower bound: the sum began partway through the session, or the
reader lost its place (the file shrank or was replaced). Sorting by RATE orders the
seats that have a number; a seat showing - or ? sorts below them.

Dollars are not in TOUT or RATE. COST (opt-in, --columns cost) is Claude Code's own
running cost total for a seat with a statusline feed, as ~$4.12, or a - when the
feed never carried one. The ~ marks an estimate: it is the harness's list-price
figure, not a bill, and under a subscription it measures usage, not money. The
per-minute change in that total has no column; marvel describe session carries it
as cost_rate (~$0.11/m). It shows a - until a full quiet window of readings exists,
which includes the first window after the total drops (a restart, /clear or a new
session starts a fresh baseline), a ? once the feed has been silent past the window
or while a silence of over three minutes is inside it, and never a negative figure.

The totals line sits under the daemon header, on a terminal or with --header, and
never in a pipe or in -o json or yaml. It sums the columns shown, so --columns brings a
field back. Each field counts only the rows whose cell shows a value, and prints how
many in parentheses; a row with no value is left out and never counted as zero, and a
field no row has a value for is a -. mCPU is the sum of the seats' millicpu. RSS is the
sum of resident set sizes, which counts shared pages once per process, so it overstates
memory. TOUT, TIN, CR, CW and PROMPT are sums of the cumulative counts since each seat
started, and a trailing + means a lower bound: the count names how many rows were
partial. RATE is the sum of fresh rates only, a ? or - adding nothing, since each
seat's rate is judged under its own window; it is not a cluster rate. COST is the sum
of the seats' running estimates at list price, with the ~ the column carries; it is not
a bill and covers statusline seats only. HIT% is never summed. TOUT and COST are
separate fields and nothing converts tokens to dollars. A wide line breaks between
fields onto a second totals line. Every total is diagnostic only: nothing gates or acts
on it.

A trailing * on a LAST-ACTIVE or ACTIVE% value means a statusline or heartbeat reported
it, which is weaker than token flow: * reported by statusline or heartbeat,
not token flow. A value from the token stream carries no mark. On a terminal
one legend line under the header says so when a mark is on screen; piped
output keeps the mark and drops the legend. A - means never measured.

--load (get teams only) prints one row per team: SEATS running of declared,
ACTIVE% as the mean over the seats that have a reading with how many that is
("62% (4 of 5)"), and LIMITED seats. It reads the daemon you reach, one cluster.
A row follows the sessions cells' three states: a value while fresh, ? once the
daemon stopped computing it, - when it never did. A team with no seat that has a
reading prints - for ACTIVE%, never 0%. With two or more teams a last row, all teams,
sums the fresh team rows: SEATS and LIMITED are sums, and ACTIVE% is the mean over every
measured seat, not a mean of the team means. Its cell reads (measured of running): the
measured seats against the running seats of every fresh team, so a fresh team with no
measured seat adds its running seats and nothing to the mean. A stale or never-observed
team adds nothing, and the label then counts the teams, as in all teams (2 of 4) for two
fresh teams of four shown. Diagnostic only: nothing gates on it.

-o json|yaml (get team, get workspace and get sessions) prints a document instead of
the table. For get team and get workspace it is two labelled sections.
desired is the store's record after scale and similar edits; it is not the file
that was applied, and no round trip to that file is promised. observed is the
live sessions at read time and is not for re-apply. Secret-looking Env values
show as (redacted) in both.

For get sessions, -o json prints the sessions as a list. With --cluster a,b or
--all-clusters it prints one object, {"clusters": [...], "sessions": [...]}: one
record per cluster with its outcome (the names the status lines use), and the
rows of the clusters that gave rows, each with a cluster field. The exit code is
the table's. Nothing is replaced for display: a name holding control or bidi
characters, which the table draws as U+FFFD, appears as the daemon sent it, so -o output is raw: a
terminal may act on such characters, and piping it through cat -v shows them.
Secret-looking Env values show as (redacted), as in the team document.

With -w, get sessions is a live view. j and k, or the down and up arrow keys, move
the cursor. Enter opens the selected session; on a remote cluster's row it
prints the attach command and runs nothing. The status line shows the marvel
attach command for the session; i shows the tmux command behind it. To leave a
session without stopping its agent, press Ctrl-B, then d; typing exit or Ctrl-D in it stops the agent.
Inside tmux the keys differ: see marvel attach --help. The column letters listed
under ? sort, ? lists every key, q quits.

```
marvel get <resource-type> [flags]
```

### Options

```
      --account string            with get account-readings: keep the accounts whose key contains this text
      --all-clusters              with get sessions or get vitals: read every cluster in the client config, in parallel, each under its own 10 s timer
      --columns string            sessions columns, comma-separated and in order (overrides display.session_columns); names: workspace, team, role, generation, name, state, health, composer, pane, context, cpu, rss, desk, runtime, llm, workdir, tout, rate, last-active, last-turn, age, active, tin, cr, cw, prompt, hit, spec, account, 5h, 7d, 5h-reset, 7d-reset, read, backend, cred, home, login, cluster, key, cost, wide, acct, service, rest
      --header                    print the daemon header above the sessions table even when stdout is not a terminal
      --load                      with get teams: one row per team with seats, mean ACTIVE% and limited seats
      --no-trunc                  print RUNTIME and WORKDIR in full instead of cutting them to fit
  -o, --output string             print get team or get workspace as json or yaml (desired and observed sections), or get sessions as json or yaml (a list; with a cluster set, {clusters, sessions})
      --since duration            with get account-readings: keep readings no older than this, as 1h or 90m, at least 1s (default: every reading held)
  -w, --watch seconds[=default]   watch sessions (optional: seconds, default 2)
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel](marvel.md)	 - Agent orchestration control plane
