# finding-060: S-2 taught three things: the control plane is not a redaction boundary, a hiding shadow field undoes cleanly, and named defaults put a design gap where someone can rule on it

Source: scheduled-runs S-2, marvel#431 (issue #430), squash-merged to main as
`f778d0b`. bd `aae-orc-kv634`. Design: `docs/design/scheduled-runs.md`
sections 5, 7 and 10. Branch commits, all on #431: red `8865eb0`, green
`b2dbafb`, review red `0f42dec`, review green `ac3a250`, docs `5c3ed94`.
Code claims checked against origin/main `f778d0b`.

**Placement.** marvel is the subject: what its read verbs may print, and how
its builds carry design gaps to a ruling.

## 1. The control and management plane is not a redaction boundary

**What I built first.** Design section 5 said of a run's result text: "It
may contain whatever the run read, including a client's data. Reports carry
pointers and status only." I read that as a reason to keep the text out of
`describe team` too. At `b2dbafb` describe printed `result_bytes` and hid the
text, and my code comment gave the reason that any seat can call describe.

**The ruling.** The operator rejected that reading. Describe exists to show
the operator the cluster's state, and team names, role names and run output
are that state. A client name on the operator's own control plane is not a
leak. I followed the ruling at `ac3a250`: describe shows `result` and
`result_truncated`, and the 4 KiB store cap stays. At `5c3ed94` I deleted
only the client-data sentence from section 5. The rule beside it stays
(`scheduled-runs.md:318`, "What is never forwarded: the result text. Reports
carry pointers and status only"). Read together, the two say where the
boundary is:

- **Inside the control plane** (the store, describe, the operator's read
  verbs): show it.
- **What marvel publishes** (events, reports, the bus mirror planned for
  S-6): status and pointers only. `run.succeeded` and `run.failed` carry no
  result text (`internal/session/run_record.go`, the emit at the end of
  `recordScheduledRun`).

**What I got wrong, as a pattern.** I extended a rule about forwarding into
a rule about reading. Nothing in section 5 said describe should hide
anything; the hiding was my inference. The reviewer then built on it, and
proposed (note 5 on #431) that the stream bridge summarize a scheduled
role's messages as role plus length. That proposal had the same root, and
it was not taken.

**Open tension, recorded and not resolved.** marvel#421 is still open:
"describe prints role Env, Args and Prompt values; marvel needs a redaction
design". Its design, `docs/design/describe-redaction.md` (on main), redacts
`Runtime.Env` values at the RPC boundary: "the durable record keeps what
marvel needs; the wire gets a view". The S-2 ruling says the control plane
is not a redaction boundary. Those two can be read as compatible: an Env
value is a credential channel, and run output is state. They can also be
read as in conflict, if "not a redaction boundary" is taken to cover Env as
well. #421's design should name which reading it takes before its code
lands. This finding does not decide it.

## 2. A shadow field hides an embedded one, and undoes cleanly

**The technique.** `encoding/json` resolves a name collision in favor of
the shallower field. To hide two fields of an embedded `api.RunRecord`, I
declared same-named fields on the outer struct with `omitempty` and left
them empty (`b2dbafb`, `internal/daemon/schedule_describe.go:37-45`).

```go
type runDescription struct {
	api.RunRecord
	Result          string `json:"result,omitempty"`
	ResultTruncated bool   `json:"result_truncated,omitempty"`
	ResultBytes     int    `json:"result_bytes"`
	Duration        string `json:"duration"`
}
```

The test asserted that the raw JSON did not contain the sample text, so the
hiding was tested and not just assumed.

**Its cost.** It hid more than intended. The reviewer noticed that
`result_truncated` was shadowed too, so describe never showed it, even
though the flag was never in question (#431 review, should-fix 4). A shadow
field hides silently: nothing in the output says a field was suppressed.

**Undoing it under a mid-review ruling.** I treated the ruling as a new
requirement and ran red and green again rather than editing the old test in
place:

- red `0f42dec`: `TestDescribeTeamShowsTheSchedule` was rewritten to assert
  that the text is present, that a 5000-byte result shows its first 4 KiB
  with `result_truncated: true`, and that a short one shows
  `result_truncated: false`. It failed at `b2dbafb`.
- green `ac3a250`: removed the shadow fields and `result_bytes`, and dropped
  `omitempty` from `RunRecord.ResultTruncated` so that `false` is shown
  (`internal/api/schedule_status.go`). The result is now a two-field struct
  (`schedule_describe.go:35-38` on main).

The first red failed by panicking, not on an assertion: a type assertion
on the missing `result` key. I made that line safe before committing, so the
red commit fails with a message, not a stack trace.

**Lesson.** If a field must not reach the wire, use an explicit view struct
that does not embed the record. Use a shadow only when hiding exactly that
field is the point, and test both what is hidden and what is still shown.

## 3. Named defaults put a design gap where someone can rule on it

S-1 (#429) listed the defaults it chose where the design was silent. I did
the same for S-2. Before the build I sent five gaps to the supervisor as a
query, built each one with a stated default, and listed them in the PR body
for ratification. Two more joined during the build and review, for seven:

1. stale anchor: `since`, the first time marvel recorded the schedule;
2. recovery reuses `schedule.stale` at info level, not a new kind;
3. result text kept out of describe: **ruled, rejected** (section 1);
4. result source: the last assistant message, so `contracts/schema` is
   unchanged;
5. `run.succeeded` and `run.failed` at reap;
6. only a run whose pane ends on its own is recorded (added during the
   build);
7. history capped at 50 runs per outcome (added from review should-fix 2,
   `internal/api/schedule.go`, `maxScheduleHistory`).

**What it bought.** The one wrong default was overturned in a single review
round of three commits. The reviewer had a list to check against, and the
operator had a sentence to reject. Without the list, default 3 would have
been an unstated behavior, found later by someone reading describe output.

**What it did not buy.** Listing a default is not a ruling. The supervisor
forwarded all seven as written without ruling on them. Only default 3 drew
a ruling, and no ruling on the other six has reached me. A listed default
can therefore sit in main unreviewed indefinitely, the same failure the
ticket rules describe for unclosed tickets. The list needs an owner who
closes it.

## What this changes

- For describe and other read verbs: the default is to show. Redaction
  applies to what marvel publishes, and to credential values only if #421's
  design says so.
- For builders: when a design rule is about forwarding, do not extend it to
  reading without asking. Prefer view structs to shadow fields.
- For PRs: keep listing named defaults. Ask the supervisor whether to track
  the six open ones on `aae-orc-kv634` or on the S-3 ticket, so they do not
  sit unratified with no owner.
