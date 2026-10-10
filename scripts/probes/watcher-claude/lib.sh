#!/usr/bin/env bash
# Sourced by run.sh. Every capture of claude's pane goes through save_capture,
# so key text, whole or in part, never reaches a file.
#
# save_capture FILE: copy stdin to FILE with key text hidden. It needs $tool (the
# built watcherprobe) and $cred (the credential, as a plain variable and never
# exported; it reaches the masker only through that one process's environment).
save_capture() {
	WATCHER_PROBE_MASK=${cred:-} "$tool" mask >"$1"
}
