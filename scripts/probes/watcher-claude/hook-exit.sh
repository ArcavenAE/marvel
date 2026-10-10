#!/usr/bin/env bash
# A UserPromptSubmit hook for check c10: it exits with the code held in the
# control file (0 when the file is absent), so the driver can see what a nonzero
# exit does to the prompt. It prints nothing.
code=0
[[ -r "${1:-}" ]] && code=$(<"$1")
exit "${code:-0}"
