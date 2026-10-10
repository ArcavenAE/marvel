#!/usr/bin/env bash
# The watcher's claude probe kit (marvel#841, docs/design/marvel-watcher.md
# section 6). It starts a scratch claude, drives the scenarios behind checks c1 to
# c13, and writes probe_result.json. It measures; it does not ring, restart or
# route anything, and it never touches a live seat, a daemon or a real claude
# config. Everything lives under one scratch directory:
#
#   HOME and CLAUDE_CONFIG_DIR   empty, inside the scratch directory
#   tmux                         its own socket, always named with -S
#   repository                   a throwaway git init
#   credential                   WATCHER_PROBE_CREDENTIAL, never a fleet seat's;
#                                it reaches only the scratch tmux server's
#                                environment, reaches claude through an
#                                apiKeyHelper, and is never printed or logged;
#                                every capture is masked before it is written
#
# usage: run.sh --scratch ABSOLUTE_DIR [--dry-run]
# env:   WATCHER_PROBE_CREDENTIAL  required for a real run
#        WATCHER_PROBE_CLAUDE      the claude binary (default: claude on PATH)
#        WATCHER_PROBE_TOOL        a built watcherprobe (default: built from this tree)
#        WATCHER_PROBE_READY       text that shows claude's prompt box ('? for shortcuts')
#        WATCHER_PROBE_IDLE_SECS   idle and quiet window length (default 60)
#
# The loggers and checker are tested; this driver is not, against a live claude.
# It stops with the pane saved on any timeout instead of guessing. C2 runs it.
set -euo pipefail

die() { echo "run.sh: $*" >&2; exit 2; }

scratch="" dry=0
while (($#)); do
	case $1 in
		--scratch) scratch=${2:-}; shift 2 ;;
		--dry-run) dry=1; shift ;;
		-h | --help) sed -n '2,24p' "${BASH_SOURCE[0]}"; exit 0 ;;
		*) die "unknown argument $1" ;;
	esac
done

[[ -n $scratch ]] || die "--scratch is required"
[[ $scratch == /* ]] || die "--scratch must be an absolute path"
[[ $scratch =~ ^[A-Za-z0-9_./-]+$ ]] || die "--scratch may hold only letters, digits and _ . / -"
[[ /$scratch/ != */../* ]] || die "--scratch may not hold .."
scratch=${scratch%/}
[[ -n $scratch && $scratch != / ]] || die "--scratch may not be /"

# Refuse any place a real claude keeps its state, before anything is created.
# Paths are compared resolved, so a symlinked component cannot lead into it, and
# the home directory comes from the passwd entry as well as from HOME.
resolve() { # an absolute path, resolved through its deepest existing ancestor
	local p=$1 rest="" d
	while [[ ! -e $p && ! -L $p ]]; do
		rest=/$(basename "$p")$rest
		p=$(dirname "$p")
	done
	d=$(cd -P "$p" 2>/dev/null && pwd -P) || return 1
	[[ $d == / ]] && d=""
	printf '%s%s\n' "$d" "$rest"
}
inside() { [[ $1 == "$2" || $1 == "$2"/* ]]; }
pw_user=$(id -un)
[[ $pw_user =~ ^[A-Za-z0-9._-]+$ ]] || die "cannot read the passwd home for user $pw_user"
pw_home=$(eval "printf %s ~$pw_user")
[[ $pw_home == /* ]] || die "cannot read the passwd home for user $pw_user"
real_scratch=$(resolve "$scratch") || die "--scratch cannot be resolved"
for home in "${HOME:-}" "$pw_home"; do
	[[ -n $home ]] || continue
	for forbidden in "$home/.claude" "$home/.config/claude"; do
		for f in "$forbidden" "$(resolve "$forbidden" 2>/dev/null || true)"; do
			[[ -n $f ]] || continue
			! inside "$scratch" "$f" && ! inside "$real_scratch" "$f" || die "--scratch is inside $forbidden"
		done
	done
	real_home=$(resolve "$home" 2>/dev/null || true)
	[[ -z $real_home ]] || ! inside "$real_home" "$real_scratch" || die "--scratch is the real home or holds it"
done
if [[ -n ${CLAUDE_CONFIG_DIR:-} ]]; then
	for f in "$CLAUDE_CONFIG_DIR" "$(resolve "$CLAUDE_CONFIG_DIR" 2>/dev/null || true)"; do
		[[ -n $f ]] || continue
		! inside "$scratch" "$f" && ! inside "$real_scratch" "$f" || die "--scratch is inside $CLAUDE_CONFIG_DIR"
	done
fi
[[ -n ${HOME:-} ]] || die "HOME is empty; the kit will not guess where a real config lives"
if [[ -e $scratch ]]; then
	[[ -d $scratch ]] || die "--scratch exists and is not a directory"
	[[ -z $(ls -A "$scratch") ]] || die "--scratch is not empty"
fi

sock=$scratch/tmux.sock events=$scratch/events.tsv result=$scratch/probe_result.json
cred_state=missing
[[ -n ${WATCHER_PROBE_CREDENTIAL:-} ]] && cred_state=supplied

if ((dry)); then
	printf '%s\n' "HOME=$scratch/home" "CLAUDE_CONFIG_DIR=$scratch/config" "TMUX_SOCKET=$sock" \
		"REPO=$scratch/repo" "EVENTS=$events" "RESULT=$result" "CREDENTIAL=$cred_state"
	exit 0
fi
[[ $cred_state == supplied ]] || die "WATCHER_PROBE_CREDENTIAL is required for a real run"

kit=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(cd "$kit/../../.." && pwd)
claude=$(command -v "${WATCHER_PROBE_CLAUDE:-claude}") || die "no claude binary"

mkdir -p "$scratch"/{home,config,repo,bin}
# shellcheck source=lib.sh
source "$kit/lib.sh"
tool=${WATCHER_PROBE_TOOL:-$scratch/bin/watcherprobe}
[[ -n ${WATCHER_PROBE_TOOL:-} ]] || (cd "$root" && go build -o "$tool" ./cmd/watcherprobe)
GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null git -C "$scratch/repo" init -q
echo "throwaway repository for the claude probe" >"$scratch/repo/README"

# From here the environment is the scratch one. Everything the caller exported,
# MARVEL_*, TMUX and the rest, is dropped; the credential moves to a plain
# variable that is never exported.
cred=$WATCHER_PROBE_CREDENTIAL
for v in $(compgen -e); do
	case $v in
		PATH | TERM | WATCHER_PROBE_*) ;;
		*) unset "$v" 2>/dev/null || true ;;
	esac
done
unset WATCHER_PROBE_CREDENTIAL
export HOME=$scratch/home CLAUDE_CONFIG_DIR=$scratch/config TERM=${TERM:-xterm-256color}
ready=${WATCHER_PROBE_READY:-'? for shortcuts'}
idle=${WATCHER_PROBE_IDLE_SECS:-60}

hookcmd="$tool hook --log $events"
event_hook() { printf '"%s":[{"hooks":[{"type":"command","command":"%s"}]}]' "$1" "$hookcmd"; }
cat >"$CLAUDE_CONFIG_DIR/settings.json" <<JSON
{
  "apiKeyHelper": "bash $kit/key-helper.sh",
  "statusLine": {"type": "command", "command": "$tool statusline --log $events", "refreshInterval": 15},
  "hooks": {
    $(event_hook SessionStart),
    "UserPromptSubmit": [{"hooks": [
      {"type": "command", "command": "$hookcmd"},
      {"type": "command", "command": "bash $kit/hook-canary.sh $scratch/canary.on"},
      {"type": "command", "command": "bash $kit/hook-exit.sh $scratch/exit.code"}
    ]}],
    $(event_hook Stop),
    $(event_hook StopFailure),
    $(event_hook PermissionDenied),
    $(event_hook Notification),
    $(event_hook SubagentStart),
    $(event_hook SubagentStop)
  }
}
JSON

tm() { tmux -S "$sock" -f /dev/null "$@"; }
mark() { "$tool" mark --log "$events" "$@"; }
cleanup() { tm kill-server 2>/dev/null || true; }
trap cleanup EXIT
stop() {
	echo "STOP: $*" >&2
	pane 2>/dev/null | save_capture "$scratch/stop-capture.txt" || true
	echo "pane saved to $scratch/stop-capture.txt" >&2
	exit 1
}
pane() { tm capture-pane -p -S - -t probe; }
wait_for() { # pattern seconds
	local t0=$SECONDS
	while ((SECONDS - t0 < $2)); do
		pane | /usr/bin/grep -qE -- "$1" && return 0
		sleep 1
	done
	return 1
}
settle() { # seconds of an unchanged pane; gives up after SETTLE_MAX
	local need=${1:-10} same=0 last="" cur t0=$SECONDS
	while ((SECONDS - t0 < ${SETTLE_MAX:-240})); do
		cur=$(pane | cksum)
		if [[ $cur == "$last" ]]; then same=$((same + 1)); else same=0 last=$cur; fi
		((same >= need)) && return 0
		sleep 1
	done
	stop "the pane never settled"
}
type_line() { tm send-keys -t probe -l "$1"; sleep 0.5; tm send-keys -t probe Enter; }
prompt() { # id text: one prompt, with its window marked
	mark prompt-sent "$1"
	type_line "$2"
	settle
	mark prompt-done "$1"
}

# The server starts with the credential in its own environment, so no argument
# list carries it, and the panes inherit it for key-helper.sh. It stays a plain
# variable so save_capture can mask it.
WATCHER_PROBE_KEY=$cred tmux -S "$sock" -f /dev/null new-session -d -s probe -x 200 -y 50 -c "$scratch/repo" "$claude"

# c1: the trust dialog, with whatever hooks fire while it is open.
mark trust-open
wait_for 'trust' 60 || stop "no trust dialog appeared"
sleep 5
tm send-keys -t probe Enter
mark trust-closed
wait_for "$ready" 60 || stop "claude never showed its prompt box"
echo "scenario c1 done"

# c2: cost and context while idle.
mark idle-start; sleep "$idle"; mark idle-end

# c3: three plain prompts, one submit and one stop each.
for n in 1 2 3; do prompt "p$n" "Reply with the single word ok."; done

# c4 and c5: a ring stand-in typed into the composer.
mark ring-sent "wake up and reply ok"
type_line "wake up and reply ok"
settle
mark ring-done

# c6: a tool call that the permission dialog can deny.
mark denial-start
type_line "Use the Bash tool to run: ls /"
wait_for 'Do you want|permission' 60 || stop "no permission dialog appeared"
tm send-keys -t probe Escape
settle
mark denial-end

# c7: an interrupt in the middle of a long answer.
type_line "Count from 1 to 300, one number per line."
sleep 5
mark interrupt-sent
tm send-keys -t probe Escape
settle 5
mark interrupt-end

# c8: no prompt at all.
mark quiet-start; sleep "$idle"; mark quiet-end

# c9: does a hook's stdout reach the context? The token is printed only by the
# hook, so seeing it in the pane after the question means claude was told it or
# the UI showed it; the capture is kept for C2 to read, since the two differ.
: >"$scratch/canary.on"
type_line "What token did a hook just print for you? Answer with the token only."
settle
rm -f "$scratch/canary.on"
pane | save_capture "$scratch/capture-c9.txt"
if /usr/bin/grep -q 'WATCHER-CANARY-7f3a91' "$scratch/capture-c9.txt"; then mark stdout-canary yes; else mark stdout-canary no; fi

# c10: what a nonzero exit does to a prompt. Each word is built from two halves
# so it appears only in a reply, never in the prompt, and each round has its own.
words=("" pineapple watermelon)
halves=("" "pine and apple" "water and melon")
for code in 1 2; do
	echo "$code" >"$scratch/exit.code"
	type_line "Reply with the one word formed by joining ${halves[$code]}."
	settle
	if pane | /usr/bin/grep -q "${words[$code]}"; then effect="ran on"; else effect=blocked; fi
	mark "exit$code-effect" "$effect"
	echo 0 >"$scratch/exit.code"
done
rm -f "$scratch/exit.code"

# c11: a subagent.
mark subagent-start
type_line "Use the Task tool to start a subagent that replies hello, then say done."
settle
mark subagent-end

# c12: a background task that outlives the turn.
mark bg-start
type_line "Run sh -c 'sleep 40; touch bgdone' as a background task with the Bash tool, then reply started."
t0=$SECONDS
until [[ -e $scratch/repo/bgdone ]]; do
	((SECONDS - t0 < 180)) || stop "the background task never finished"
	sleep 1
done
mark bg-task-done
settle
mark bg-end

stamp="$("$claude" --version 2>&1 | head -1) sha256:$(shasum -a 256 "$claude" | cut -c1-12)"
pane | save_capture "$scratch/pane-final.txt"
"$tool" check --log "$events" --out "$result" --stamp "$stamp"
echo "wrote $result"
