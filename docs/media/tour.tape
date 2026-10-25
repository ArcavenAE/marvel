# The marvel tour, recorded with VHS (https://github.com/charmbracelet/vhs).
# Run it from the repository root, inside a scratch container or VM, after
# `just build`: it starts a real daemon and real sessions on the simulator.
#   vhs docs/media/tour.tape
# The beats match docs/tour.md. Outputs: the mp4 and the four stills are
# published; the text dump is only for checking the frames before they are
# committed. The README gif comes from docs/media/hero.tape.

Output docs/media/tour.mp4
Output out/tour.txt

Set Shell bash
Set Width 1300
Set Height 900
Set FontSize 14
Set TypingSpeed 35ms
Set Padding 16
Set WindowBar Colorful

# Setup, not shown: one tmux session with two stacked panes. The top pane
# watches the team; the bottom pane is where commands are typed.
Hide
Type `export PS1='$ ' PATH="$PWD/bin:$PATH" && clear` Enter
Type `tmux -f /dev/null new-session -d -s tour -x "$(tput cols)" -y "$(tput lines)" "bash --norc" && tmux set -t tour status off && tmux set -t tour pane-border-style fg=colour240 && tmux split-window -v -t tour -l 26 "bash --norc"` Enter
Type `tmux send-keys -t tour:0.0 "PS1=''; clear; until marvel get sessions >/dev/null 2>&1; do sleep 0.3; done; clear; marvel get sessions -w 1 --columns role,generation,name,state,health,context,age" Enter` Enter
Type `tmux send-keys -t tour:0.1 "PS1='$ '; clear" Enter && tmux select-pane -t tour:0.1 && tmux attach -t tour` Enter
Sleep 2s
Show

# 0:00 title
Type "# marvel keeps a declared team of AI agent sessions running:" Enter
Type "# it starts them, watches them, repairs them and rotates them." Enter
Sleep 4s
Hide
Type "clear" Enter
Show

# 0:10 the manifest, shown full height
Hide
Ctrl+B
Type "z"
Show
Type "cat examples/tour.toml" Enter
Sleep 9s
Hide
Ctrl+B
Type "z"
Type "clear" Enter
Show

# 0:25 start the daemon and make the manifest true
Type "marvel daemon 2>/dev/null &" Enter
Sleep 2s
Type "marvel work examples/tour.toml" Enter
Sleep 4s
Screenshot docs/media/tour-1-work.png

# 0:45 heartbeats land, CTX% climbs
Sleep 10s

# 1:00 lose a session
Type "marvel kill demo/crew-worker-g1-1" Enter
Sleep 4s
Screenshot docs/media/tour-2-kill.png
Sleep 3s

# 1:20 scale the workers
Type "marvel scale demo/crew --role worker --replicas 5" Enter
Sleep 5s
Screenshot docs/media/tour-3-scale.png
Sleep 2s

# 1:35 shift to a new generation
Type "marvel shift demo/crew" Enter
Sleep 8s
Screenshot docs/media/tour-4-shift.png
Sleep 20s

# 2:05 the record
Type "clear" Enter
Type "marvel events" Enter
Sleep 8s

# 2:20 tear it down (the watch is closed first, so it does not print a reconnect warning)
Hide
Type `tmux send-keys -t tour:0.0 q` Enter
Type "clear" Enter
Show
Type "marvel stop --teardown" Enter
Sleep 3s

# 2:30 closing card
Type "clear" Enter
Type "# same commands for Claude Code, Codex or opencode seats" Enter
Type "# brew install arcavenae/tap/marvel" Enter
Type "# docs: github.com/ArcavenAE/marvel/blob/main/docs/tour.md" Enter
Sleep 5s
