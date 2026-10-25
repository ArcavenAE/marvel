# The README hero: a session is killed and replaced, then the team shifts to
# a new generation. Run it from the repository root, inside a scratch
# container or VM, after `just build`:
#   vhs docs/media/hero.tape

Output docs/media/tour.gif

Set Shell bash
Set Width 1300
Set Height 560
Set FontSize 14
Set TypingSpeed 35ms
Set Padding 16
Set WindowBar Colorful

Hide
Type `export PS1='$ ' PATH="$PWD/bin:$PATH" && clear` Enter
Type `tmux -f /dev/null new-session -d -s hero -x "$(tput cols)" -y "$(tput lines)" "bash --norc" && tmux set -t hero status off && tmux set -t hero pane-border-style fg=colour240 && tmux split-window -v -t hero -l 5 "bash --norc"` Enter
Type `tmux send-keys -t hero:0.0 "PS1=''; clear; until marvel get sessions >/dev/null 2>&1; do sleep 0.3; done; clear; marvel get sessions -w 1 --columns role,generation,name,state,health,context,age" Enter` Enter
Type `tmux send-keys -t hero:0.1 "PS1='$ '; clear" Enter && tmux select-pane -t hero:0.1 && tmux attach -t hero` Enter
Sleep 2s
Type "marvel daemon 2>/dev/null &" Enter
Type "marvel work examples/tour.toml" Enter
Sleep 1s
Show
Sleep 8s
Type "marvel kill demo/crew-worker-g1-1" Enter
Sleep 6s
Type "marvel shift demo/crew" Enter
Sleep 22s
