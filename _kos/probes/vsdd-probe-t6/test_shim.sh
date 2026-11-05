#!/bin/bash
# Unit tests for shim, against testdata/fake-marvel. No network, no marvel.
# Run: bash _kos/probes/vsdd-probe-t6/test_shim.sh
set -u
here="$(cd "$(dirname "$0")" && pwd)"
shim="$here/shim"
fails=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1: $2"; fails=$((fails + 1)); }

new_env() {
  FAKE_DIR=$(mktemp -d)
  export FAKE_DIR
  export MARVEL="$here/testdata/fake-marvel"
  export PROBE_WORKSPACE=probe PROBE_RUN_ID=r1 PROBE_WORKDIR=/work/proj
  export PROBE_MODEL=model-x PROBE_PERMISSION_MODE=auto
  unset PROBE_BRANCH_KEY PROBE_EXTRA_ENV FAKE_RUN_FAIL FAKE_BAD_ARGS FAKE_DAEMON_DOWN FAKE_WAIT_SLEEP PROBE_AWAIT_LIMIT
  export PROBE_WAIT_TIMEOUT=1s
  prompt="$FAKE_DIR/prompt.md"
  echo "go" >"$prompt"
}
seats() { ls "$FAKE_DIR" | grep -c '^seat\.' || true; }

# dispatching twice creates one seat, and both calls read as dispatched
new_env
a=$("$shim" dispatch r1-c1-w1-g1-a1 "$prompt"); b=$("$shim" dispatch r1-c1-w1-g1-a1 "$prompt")
if [[ "$a" == "dispatched r1-c1-w1-g1-a1" && "$b" == "$a" && "$(seats)" == 1 ]]; then pass "dispatching twice creates one seat"
else fail "dispatching twice creates one seat" "a=[$a] b=[$b] seats=$(seats)"; fi

# the key is deterministic: it is the seat's name, and the seat's env, with the pinned flags
new_env
"$shim" dispatch r1-c2-w3-g4-a1 "$prompt" >/dev/null
got=$(tr '\n' ' ' <"$FAKE_DIR/run.r1-c2-w3-g4-a1.args")
want="run --experimental --mode headless --workspace probe --team arm-b-r1 --role branch-run --name r1-c2-w3-g4-a1 --workdir /work/proj --prompt-file $prompt --env PROBE_BRANCH_KEY=r1-c2-w3-g4-a1 -- claude --agent vsdd-orchestrator --model model-x --permission-mode auto "
got="run $got"
if [[ "$got" == "$want" ]]; then pass "the key is deterministic and the flags are pinned"
else fail "the key is deterministic and the flags are pinned" "got [$got]"; fi

# extra env pairs travel as repeated --env
new_env
PROBE_EXTRA_ENV="A=1 B=2" "$shim" dispatch k1 "$prompt" >/dev/null
if tr '\n' ' ' <"$FAKE_DIR/run.k1.args" | grep -q -- '--env A=1 --env B=2'; then pass "extra env pairs travel as --env"
else fail "extra env pairs travel as --env" "$(tr '\n' ' ' <"$FAKE_DIR/run.k1.args")"; fi

# any other dispatch error prints refused with marvel's message
new_env
out=$(FAKE_RUN_FAIL="admission refused: max_sessions=3" "$shim" dispatch k2 "$prompt"); rc=$?
if [[ "$out" == "refused admission refused: max_sessions=3" && $rc -eq 1 && "$(seats)" == 0 ]]; then pass "an admission refusal prints refused with marvel's message"
else fail "an admission refusal prints refused with marvel's message" "out=[$out] rc=$rc"; fi

# dispatch refuses under PROBE_BRANCH_KEY
new_env
out=$(PROBE_BRANCH_KEY=k0 "$shim" dispatch k3 "$prompt"); rc=$?
if [[ "$out" == refused* && $rc -eq 1 && "$(seats)" == 0 && ! -e "$FAKE_DIR/run.calls" ]]; then pass "dispatch refuses under PROBE_BRANCH_KEY"
else fail "dispatch refuses under PROBE_BRANCH_KEY" "out=[$out] rc=$rc"; fi

# a bad key or a missing prompt is refused before marvel is called
new_env
o1=$("$shim" dispatch 'Bad/Key' "$prompt"); o2=$("$shim" dispatch k4 /nonexistent)
if [[ "$o1" == refused* && "$o2" == refused* && ! -e "$FAKE_DIR/run.calls" ]]; then pass "a bad key or missing prompt is refused before marvel"
else fail "a bad key or missing prompt is refused" "o1=[$o1] o2=[$o2]"; fi

# await moves pending to succeeded, with the harness session id
new_env
"$shim" dispatch k5 "$prompt" >/dev/null
p=$("$shim" await k5)
echo succeeded >"$FAKE_DIR/state.probe.k5"
s=$("$shim" await k5)
if [[ "$p" == "k5 pending sid-k5" && "$s" == "k5 succeeded sid-k5" ]]; then pass "await moves pending to succeeded"
else fail "await moves pending to succeeded" "p=[$p] s=[$s]"; fi

# await on two keys returns within one bound, not the sum
new_env
export FAKE_WAIT_SLEEP=2
"$shim" dispatch ka "$prompt" >/dev/null; "$shim" dispatch kb "$prompt" >/dev/null
t0=$(date +%s); out=$("$shim" await ka kb); t1=$(date +%s)
if [[ $((t1 - t0)) -lt 4 && "$(printf '%s\n' "$out" | grep -c pending)" == 2 ]]; then pass "await on two keys returns within one bound"
else fail "await on two keys returns within one bound" "took $((t1 - t0))s out=[$out]"; fi

# the overall limit ends a wait that would run long; the keys read pending
new_env
export FAKE_WAIT_SLEEP=6 PROBE_AWAIT_LIMIT=1
"$shim" dispatch kc "$prompt" >/dev/null
t0=$(date +%s); out=$("$shim" await kc); t1=$(date +%s)
if [[ $((t1 - t0)) -lt 4 && "$out" == "kc pending sid-kc" ]]; then pass "the overall limit ends a long wait as pending"
else fail "the overall limit ends a long wait as pending" "took $((t1 - t0))s out=[$out]"; fi

# a failure triggers no retry: one run call, and the word is failed
new_env
"$shim" dispatch kd "$prompt" >/dev/null
echo failed >"$FAKE_DIR/state.probe.kd"
out=$("$shim" await kd)
if [[ "$out" == "kd failed sid-kd" && "$(wc -l <"$FAKE_DIR/run.calls" | tr -d ' ')" == 1 ]]; then pass "a failure triggers no retry"
else fail "a failure triggers no retry" "out=[$out] runs=$(wc -l <"$FAKE_DIR/run.calls")"; fi

# the exit codes map to the five words
new_env
for k in s1 f1 c1 r1x; do "$shim" dispatch "$k" "$prompt" >/dev/null; done
echo succeeded >"$FAKE_DIR/state.probe.s1"; echo failed >"$FAKE_DIR/state.probe.f1"; echo crashed >"$FAKE_DIR/state.probe.c1"
out=$("$shim" await s1 f1 c1 r1x nokey)
want=$'s1 succeeded sid-s1\nf1 failed sid-f1\nc1 crashed sid-c1\nr1x pending sid-r1x\nnokey unknown -'
if [[ "$out" == "$want" ]]; then pass "exit codes map to succeeded, failed, crashed, pending, unknown"
else fail "exit codes map to the five words" "out=[$out]"; fi

# exit 5 (daemon error) reads pending
new_env
"$shim" dispatch ke "$prompt" >/dev/null
out=$(FAKE_DAEMON_DOWN=1 "$shim" await ke)
if [[ "$out" == "ke pending sid-ke" ]]; then pass "wait exit 5 reads pending"
else fail "wait exit 5 reads pending" "out=[$out]"; fi

# exit 1 without the state word (a refused flag) never reads as failed
new_env
"$shim" dispatch kf "$prompt" >/dev/null
out=$(FAKE_BAD_ARGS=1 "$shim" await kf)
if [[ "$out" == "kf pending sid-kf" ]]; then pass "wait exit 1 without a state word reads pending, not failed"
else fail "wait exit 1 without a state word reads pending" "out=[$out]"; fi

# PROBE_EXTRA_ENV pairs are never globbed: a value with * or ? reaches marvel literally
new_env
gd=$(mktemp -d); : >"$gd/A=x"
( cd "$gd" && PROBE_EXTRA_ENV='A=* B=?' "$shim" dispatch kg "$prompt" >/dev/null )
args=$(tr '\n' ' ' <"$FAKE_DIR/run.kg.args")
if [[ "$args" == *"--env A=* --env B=? "* && "$args" != *"A=x"* ]]; then pass "PROBE_EXTRA_ENV is not globbed"
else fail "PROBE_EXTRA_ENV is not globbed" "args=[$args]"; fi

echo "failures: $fails"
[[ $fails -eq 0 ]]
