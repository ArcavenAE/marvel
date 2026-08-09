#!/usr/bin/env python3
"""Time one marvel shift, decomposed by stage. See finding-017.

Usage: shift_timing.py <workspace> <team> [label]
Reads MARVEL_SOCKET. Prints one JSON object on stdout.

Two clocks, deliberately:

  * The daemon's own event-ring timestamps (RFC3339, sub-second) for
    everything marvel emits an event for: team.shift-started,
    session.created, session.deleted, team.shift-completed. These are
    authoritative. `marvel events` renders at 15:04:05, which would
    quantize every interval here to a whole second, so this talks to the
    socket directly.

  * A local 50 Hz poll of the session table for the READINESS
    transition, because marvel emits no event when a successor becomes
    ready. That absence is a result, not an implementation detail: the
    moment the control plane decides the successor can take over is the
    single most load-bearing instant in a handoff, and it is not
    observable from the event ring.
"""

import json
import os
import socket
import sys
import time
from datetime import datetime


def call(method, params=None):
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.connect(os.environ["MARVEL_SOCKET"])
    req = {"method": method}
    if params is not None:
        req["params"] = params
    s.sendall((json.dumps(req) + "\n").encode())
    buf = b""
    while not buf.endswith(b"\n"):
        chunk = s.recv(65536)
        if not chunk:
            break
        buf += chunk
    s.close()
    return json.loads(buf.decode())


def ts(v):
    if not v or v.startswith("0001"):
        return None
    return datetime.fromisoformat(v.replace("Z", "+00:00")).timestamp()


def sessions():
    return call("get", {"resource_type": "sessions"}).get("result") or []


def teams():
    return call("get", {"resource_type": "teams"}).get("result") or []


def events(n=600):
    return call("events", {"n": n}).get("result", {}).get("events") or []


def run_shift(workspace, team, label, poll_hz=50.0):
    interval = 1.0 / poll_hz
    mine = lambda s: s["Workspace"] == workspace and s["Team"] == team  # noqa: E731

    old_gens = sorted({s["Generation"] for s in sessions() if mine(s)})
    seq_floor = max([e["seq"] for e in events(5)] or [0])

    t_req = time.time()
    r = call("shift", {"team_key": f"{workspace}/{team}"})
    if r.get("error"):
        raise RuntimeError(f"shift refused: {r['error']}")
    t_ret = time.time()

    marks = {}
    new_gen = None
    deadline = t_req + 900
    while time.time() < deadline:
        ss = [s for s in sessions() if mine(s)]
        now = time.time()
        newer = sorted({s["Generation"] for s in ss} - set(old_gens))
        if newer and new_gen is None:
            new_gen = max(newer)
        if new_gen is not None:
            ng = [s for s in ss if s["Generation"] == new_gen]
            if ng and all(s["State"] == "running" for s in ng):
                marks.setdefault("obs_running", now)
            if ng and all(s["State"] == "running" and ts(s.get("LastHeartbeat")) for s in ng):
                marks.setdefault("obs_heartbeat", now)
        if new_gen is not None and not [s for s in ss if s["Generation"] in old_gens]:
            marks.setdefault("obs_predecessor_gone", now)
        tt = [t for t in teams() if t["Workspace"] == workspace and t["Name"] == team]
        phase = (tt[0].get("Shift") or {}).get("Phase", "") if tt else ""
        if not phase and "obs_predecessor_gone" in marks:
            marks["obs_cleared"] = now
            break
        time.sleep(interval)

    evs = [e for e in events(600)
           if e["seq"] > seq_floor and e.get("workspace") == workspace]

    def first(kind, pred=lambda e: True):
        for e in evs:
            if e["kind"] == kind and pred(e):
                return ts(e["ts"])
        return None

    ev_start = first("team.shift-started")
    ev_created = first("session.created",
                       lambda e: f"-g{new_gen}-" in (e.get("session") or ""))
    ev_deleted = first("session.deleted")
    ev_done = first("team.shift-completed")
    ev_timeout = first("team.shift-timed-out")

    # NOTE: the successor's LastHeartbeat is NOT a first-heartbeat stamp.
    # It is overwritten every beat, so reading it after the shift returns
    # the most recent one. Only the poll can see the first. Recorded so
    # the two are never confused.
    succ = [s for s in sessions() if mine(s) and s["Generation"] == new_gen]
    sv_created = min([ts(s["CreatedAt"]) for s in succ]) if succ else None

    base = ev_start or t_req
    rec = {
        "label": label,
        "generation": new_gen,
        "clean": ev_done is not None and ev_timeout is None,
        "rpc_return_s": round(t_ret - t_req, 3),
    }
    for name, v in [
        ("t_shift_started", ev_start),
        ("t_successor_created", ev_created or sv_created),
        ("t_successor_running_obs", marks.get("obs_running")),
        ("t_successor_heartbeat_obs", marks.get("obs_heartbeat")),
        ("t_predecessor_deleted", ev_deleted),
        ("t_shift_completed", ev_done),
        ("t_shift_cleared_obs", marks.get("obs_cleared")),
    ]:
        rec[name] = round(v - base, 3) if v is not None else None
    return rec


if __name__ == "__main__":
    ws, tm = sys.argv[1], sys.argv[2]
    lbl = sys.argv[3] if len(sys.argv) > 3 else "run"
    print(json.dumps(run_shift(ws, tm, lbl)))
