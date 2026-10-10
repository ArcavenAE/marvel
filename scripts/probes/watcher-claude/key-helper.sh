#!/usr/bin/env bash
# claude's apiKeyHelper for the probe: it prints the key the scratch tmux server
# was started with. Using a helper means claude is never given the key in its own
# environment, so it has no custom-key dialog that could show a piece of it.
printenv WATCHER_PROBE_KEY
