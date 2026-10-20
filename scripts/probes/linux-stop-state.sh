#!/usr/bin/env bash
# Linux stop-state probe for the fleet-suspend gate
# (docs/design/fleet-suspend-resume.md section 1, aae-orc-ct0ff).
#
# Reruns the Darwin arms of _kos/findings/finding-marvel-1kg8 section 1 on
# Linux, against sleep and sh pane processes on a scratch tmux server with its
# own socket (TMUX unset, never the default server). It measures the tmux
# substrate, not a harness: no harness runs here.
#
# It reports and never fails on a result. A nonzero exit means the rig broke.
# A pid counts as stopped only when `ps -o stat` reads T, as in the finding.
#
# Usage: linux-stop-state.sh [label]
#   label  printed beside every result, e.g. "hosted runner" (default: local)
#   REPS   repetitions per arm (default 3)
set -uo pipefail

[[ "$(uname -s)" == Linux ]] || { echo "rig error: this probe is for Linux" >&2; exit 2; }
LABEL="${1:-local}"
REPS="${REPS:-3}"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/stopstate.XXXXXX") || exit 2
SOCK="$WORK/tmux.sock"
RECORDS="$WORK/records.tsv"
unset TMUX
SERVER=""
SIGNALLED=()

# Every tmux call is bounded, so a server left stopped by a broken arm cannot
# hang the run.
tm() { timeout 10 tmux -S "$SOCK" "$@"; }

cleanup() {
	# Resume the server and everything signalled before killing anything:
	# kill-server against a stopped server would wait for it.
	[[ -n "$SERVER" ]] && kill -CONT "$SERVER" 2>/dev/null
	for p in "${SIGNALLED[@]}"; do kill -CONT "$p" 2>/dev/null; done
	tm kill-server 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

# A trace of every command goes to a file, and a rig error prints its tail, so
# a broken run says where it broke.
exec 9>"$WORK/trace"
BASH_XTRACEFD=9
set -x

die() {
	echo "rig error: $*" >&2
	echo "--- last 40 traced commands ---" >&2
	tail -n 40 "$WORK/trace" >&2
	exit 2
}

st() { ps -o stat= -p "$1" 2>/dev/null | cut -c1; }

stop() {
	SIGNALLED+=("$2")
	kill "-$1" "$2"
}

# rec ARM REP PID AT: read the pid's state now and record it at offset AT.
rec() {
	local s
	s=$(st "$3")
	printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "${s:-gone}" | tee -a "$RECORDS"
}

# sample ARM REP PID OFFSET...: offsets are seconds after the signal, ascending.
sample() {
	local arm="$1" rep="$2" pid="$3" prev=0 at
	shift 3
	for at in "$@"; do
		sleep "$(awk -v a="$at" -v b="$prev" 'BEGIN { printf "%.3f", a - b }')"
		rec "$arm" "$rep" "$pid" "+${at}s"
		prev="$at"
	done
}

# new_pane CMD: open a window running CMD and print its pane pid.
new_pane() { tm new-window -d -P -F '#{pane_pid}' "$1"; }

# child_of PID NAME: wait up to 2 s for a child of PID named NAME, print it.
child_of() {
	local c
	for _ in $(seq 20); do
		c=$(pgrep -P "$1" -x "$2" | head -1)
		[[ -n "$c" ]] && { echo "$c"; return 0; }
		sleep 0.1
	done
	return 1
}

resume_and_check() { # ARM REP PID...
	local arm="$1" rep="$2" p
	shift 2
	for p in "$@"; do kill -CONT "$p" 2>/dev/null; done
	[[ -n "$SERVER" ]] && kill -CONT "$SERVER"
	sleep 2
	for p in "$@"; do rec "$arm" "$rep" "$p" "resume+2s"; done
	tm display -p '#{pid}' >/dev/null || die "the tmux server did not answer after resume"
}

echo "## environment ($LABEL)"
echo "label: $LABEL"
echo "uname -r: $(uname -r)"
echo "uname -m: $(uname -m)"
echo "os: $(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME")"
echo "tmux -V: $(tmux -V)"
echo "ps: $(ps --version 2>&1 | head -1)"
echo "runner image: ${ImageOS:-n/a} ${ImageVersion:-n/a}"
echo "reps per arm: $REPS"
echo

cat > "$WORK/setsid-child.sh" <<'CHILD'
setsid sh -c 'echo $$ > "$1"; exec sleep 600' child "$1" &
wait
CHILD

tm new-session -d -s probe -x 120 -y 40 'sleep 3600' || die "cannot start the scratch tmux server"
SERVER=$(tm display -p '#{pid}') || die "cannot read the server pid"
echo "scratch tmux server pid $SERVER"
printf 'arm\trep\tpid\tat\tstat\n' | tee "$RECORDS"

for rep in $(seq "$REPS"); do
	# Control: a stopped process with no tmux above it stays T. Shows the read
	# works. The sleep is reparented away from this shell, so the shell never
	# sees a child of its own stop (the first hosted run broke on that).
	p=$(sh -c 'sleep 600 >/dev/null 2>&1 & echo $!') || die "control sleep"
	stop STOP "$p"
	sample control-no-tmux "$rep" "$p" 0.05 1 5
	kill -CONT "$p"; kill "$p"

	# Arm 1: SIGSTOP to the pane's own process with tmux running.
	# Darwin: undone by the server within a second.
	p=$(new_pane 'sleep 600') || die "new pane"
	stop STOP "$p"
	sample 1-pane-stop "$rep" "$p" 0.05 0.2 0.5 1 2 5
	tm capture-pane -p -t probe >/dev/null
	rec 1-pane-stop "$rep" "$p" "after-capture"
	resume_and_check 1-pane-stop "$rep" "$p"

	# Arm 1b: SIGTSTP to the pane's own process. Darwin: did not stop it.
	p=$(new_pane 'sleep 600') || die "new pane"
	stop TSTP "$p"
	sample 1b-pane-tstp "$rep" "$p" 0.2 1 5
	resume_and_check 1b-pane-tstp "$rep" "$p"

	# Arm 2: the server first, then the pane process. Darwin: holds.
	# No tmux call while the server is stopped.
	p=$(new_pane 'sleep 600') || die "new pane"
	stop STOP "$SERVER"
	stop STOP "$p"
	sample 2-server-first "$rep" "$p" 0.1 0.2 1 5 10 20
	rec 2-server-first "$rep" "$SERVER" "server+20s"
	resume_and_check 2-server-first "$rep" "$p"

	# Arm 3: the reverse order, pane process then server. Darwin: undone
	# within 50 ms. Two gaps: back to back, and 100 ms as a tree walk would take.
	for gap in 0 0.1; do
		p=$(new_pane 'sleep 600') || die "new pane"
		stop STOP "$p"
		sleep "$gap"
		stop STOP "$SERVER"
		sample "3-reverse-gap${gap}" "$rep" "$p" 0.05 0.2 1 5
		resume_and_check "3-reverse-gap${gap}" "$rep" "$p"
	done

	# Arm 4: a child one level below a wrapper sh, stopped alone with tmux
	# running. Darwin: stays T. Control: the wrapper alone, resumed within 1 s.
	w=$(new_pane "sh -c 'sleep 600 & wait'") || die "new pane"
	c=$(child_of "$w" sleep) || die "no child under the wrapper"
	stop STOP "$c"
	sample 4-wrapper-child "$rep" "$c" 1 5 10
	tm resize-pane -t probe -x 100 >/dev/null 2>&1
	tm capture-pane -p -t probe >/dev/null
	sample 4-wrapper-child "$rep" "$c" 20
	resume_and_check 4-wrapper-child "$rep" "$c"
	stop STOP "$w"
	sample 4c-wrapper-alone "$rep" "$w" 0.2 1 2
	resume_and_check 4c-wrapper-alone "$rep" "$w"

	# Arm 5: a setsid child of a wrapper (its own session), stopped alone with
	# tmux running. The child writes its pid, since setsid may fork and leave
	# it reparented away from the wrapper.
	pidf="$WORK/setsid.$rep.pid"
	w=$(new_pane "sh $WORK/setsid-child.sh $pidf") || die "new pane"
	for _ in $(seq 20); do [[ -s "$pidf" ]] && break; sleep 0.1; done
	c=$(cat "$pidf" 2>/dev/null) || die "no setsid child pid"
	echo "arm 5 rep $rep: wrapper sid $(ps -o sid= -p "$w" | tr -d ' '), child sid $(ps -o sid= -p "$c" | tr -d ' ')"
	stop STOP "$c"
	sample 5-setsid-child "$rep" "$c" 1 5
	resume_and_check 5-setsid-child "$rep" "$c"

	tm kill-window -a -t probe:0 >/dev/null 2>&1
done

echo
echo "## summary ($LABEL, $(uname -r), $(tmux -V))"
echo "For each arm, how many reps read T at each offset."
awk -F'\t' 'NR > 1 && $4 != "server+20s" {
	key = $1 "\t" $4
	if (!(key in n)) order[++k] = key
	n[key]++
	if ($5 == "T") t[key]++
}
END {
	for (i = 1; i <= k; i++) {
		split(order[i], f, "\t")
		printf "%-22s %-14s T in %d/%d\n", f[1], f[2], t[order[i]] + 0, n[order[i]]
	}
}' "$RECORDS"
awk -F'\t' '$4 == "server+20s" { n++; if ($5 == "T") t++ }
END { printf "%-22s %-14s T in %d/%d\n", "2-server-first", "server+20s", t + 0, n }' "$RECORDS"
