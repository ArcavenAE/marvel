#!/usr/bin/env bash
# A UserPromptSubmit hook for check c9: while the control file exists it prints
# a token to stdout, so the driver can ask claude whether it reached the context.
# With no control file it prints nothing.
[[ -e "${1:-}" ]] && printf 'WATCHER-CANARY-7f3a91\n'
exit 0
