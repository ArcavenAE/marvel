#!/usr/bin/env bash
# claude's apiKeyHelper for the probe: it prints the key the scratch tmux server
# was started with, WATCHER_PROBE_KEY. Using a helper avoids ANTHROPIC_API_KEY,
# so claude shows no custom-key dialog that could print a piece of the key. It
# does not take the key out of claude's environment: claude and its hooks inherit
# WATCHER_PROBE_KEY from the tmux server, and so do Bash-tool commands unless
# something scrubs them. The loggers and the checker mask it from every file.
printenv WATCHER_PROBE_KEY
