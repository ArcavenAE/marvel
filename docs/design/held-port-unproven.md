# A held port whose child cannot be proven

Proposal, 2026-10-09. Code read at marvel main `adf6c65`. Builds on `docs/design/bus-pidfile-identity.md` (#794), section 5 (V11), and the interim ruling for #799: V11 (b), "port held, child unproven".

- Author: the architect seat, team arcaven.
- Status: design for the operator. Nothing here is ratified; section 9 carries the decisions.
- Builder: red tests first, then green, after the rulings and this design merge.

## 0. Why

Under V11 (b), the first daemon start after the upgrade that adds the identity sidecar finds a broker the old binary spawned. Marvel cannot prove that process is its own, so it neither adopts nor signals it, and the bus stays down until the operator stops the broker by hand. That is safe, and it costs one manual stop per cluster. The operator asked for "improved handling for this ... more reliably".

The party found that reliability cannot come from proving the legacy holder: nothing the kernel or the listener reports shows that an older marvel started it. It comes from three things that need no proof: noticing when the port frees and spawning then, a narrow use of a holder whose facts all match without ever signalling it, and a checked manual path.

Method: a three-round party with a casting call (runtime, messaging, security and observability seats; topology on call). Every load-bearing claim was checked by one command against marvel source or nats-server v2.14.0 documentation, and every item was put to a forced vote in the last round. The record is a party record in a private repository; this file is the result.

## 1. Four acts, four thresholds

Each act has its own bar, and evidence for a lower act never carries up to a higher one.

| act | what it may do | its bar |
|---|---|---|
| read | kernel facts, as the daemon's own uid, with no connection | none beyond the uid |
| use | connect and publish; never reload | section 4's corroboration rule |
| signal | SIGHUP, SIGTERM, SIGKILL | a recorded identity, start time included (#794 section 4). Never a legacy pidfile, never a pid found from a port or a name |
| write an identity record | the sidecar | only for a child marvel spawned |

## 2. Socket ownership asks about one pid

The owner read asks "does the pidfile pid P hold port N", never "who holds port N". It answers `owner=yes`, `owner=no`, or `owner=unknown:<cause>`. Only `owner=yes` passes; `no` and every `unknown` refuse. It never leads to a signal.

It is a new refusal at adopt, inside the new-format proof of #794. It is not added at `terminate` of marvel's own child.

- darwin: `lsof` by its absolute path (`/usr/sbin/lsof`), a fixed environment, a 2 s timeout, filtered to the one pid, the one TCP port and the listen state. The output is parsed for the one pid. lsof output is never logged; only the `owner=` answer is printed.
- linux: `/proc/net/tcp` (and `tcp6`) for the listening inode, then `/proc/<pid>/fd` for that inode. The same rule holds: what these files contain is never logged; only the `owner=` answer is printed.

## 3. Spawn when the port frees

While the bus is down as "port held, child unproven", marvel re-observes passively: kernel reads and the existing listener test, never a credential. When the port is free and no pidfile pid is alive, the existing `Start` path spawns.

- Cadence: a floor of 30 s, proposed 60 s, backing off to 10 min. The numbers are measured before shipping (section 7).
- It acts on an observed fact, not on a clock. If another process wins the race for the port, the spawn fails to bind and nothing is signalled.

## 4. Adopt-for-use of a corroborated legacy holder

Behind a code constant that defaults off until section 7's measurements pass. When on, a legacy holder may be used, never signalled, when every fact below matches:

1. a pidfile exists;
2. the owner of the listen port is the pidfile pid (section 2), and so is the owner of the monitor port;
3. the holder runs as the daemon's uid;
4. the executable base name is `nats-server`;
5. the config argument is exactly this daemon's conf path;
6. the holder's start time is not after the pidfile's modification time;
7. before any credential is sent: the server's INFO line, read without sending CONNECT, and the monitor endpoint's domain and store directory. These can only refuse.

Design rules for this path:

- the health check runs only after corroboration passes;
- the INFO read sends no client bytes, guarded by a test against a fake listener that fails on any byte received;
- use never signals and never writes an identity record;
- the holder reads as `used-unproven`, never `proven`, and the code alone tells the two apart.

Vote 3-1. The observability seat dissented: a credential must not reach a holder whose identity is unproven. That seat's alternative is section 3 and the manual path alone.

## 5. Drift

A legacy holder is never reloaded, so a broker user added after it started cannot connect to it. That state has its own code, `holder-predates-auth`: existing users keep using the bus, the bus is not ready for a new apply that needs the added user, and the remedy is the operator's stop.

## 6. The record

- A `bus.holder` cell (`asof.Cell`, per `docs/design/seat-register.md`) with a closed code list: `legacy-no-sidecar`, `sidecar-mismatch:<field>`, `sidecar-corrupt`, `owner-differs`, `used-unproven`, `holder-predates-auth`. It ages to `?` like every register cell.
- The event fires on a change of code only, never per re-observe.
- What may be printed about a holder: pid, port, whether the uid matches, the executable base name, start time, whether the conf path matches (a yes or no, never the path's neighbours), the code and the remedy. Never its argument list or environment. Vote 3-1; the observability seat preferred yes/no values, pid and codes only.

## 7. Two operator scripts

Both are runnable scripts with coded PASS or STOP checks.

1. **Scratch measurement**, run by the operator on a darwin host and in Linux CI, against throwaway brokers on scratch ports and a scratch store, never the live bus. It measures the owner read (including the monitor port), the re-observe cost at the proposed cadence, and each corroboration fact against a matching and a mismatching holder. A store-collision step is information only. Its PASS gates both section 4's constant and section 3's cadence.
2. **Recovery check**, read-only, run by the operator on the live host when the bus reads "port held, child unproven". It reports the pidfile, the sidecar state, the owner read and the code. It signals nothing; the stop stays the operator's act.

## 8. Later

- TLS on the client listener of brokers marvel spawns, filed as its own flat ticket, unprioritised.

## 9. Decisions for the operator

Each recommendation is valid until 2026-10-23 or the operator's ruling, whichever comes first; the architect seat re-checks it then. Nothing takes effect on silence.

1. **Spawn when the port frees** (section 3). (a) re-observe passively and spawn on a free port, cadence measured first; (b) report only, the operator restarts by hand. Party 4-0 for (a). Recommendation: (a).
2. **Adopt-for-use of a corroborated legacy holder** (section 4). (a) allow, behind a code constant that stays off until the measurements pass; (b) forbid, section 3 and the manual path only. Party 3-1 for (a), dissent above. Recommendation: (a), constant off until the measurements pass.
3. **Run the scratch measurement** (section 7.1). (a) yes; (b) no, and decisions 1 and 2 stay off. Recommendation: (a).
4. **What may be printed about a holder** (section 6). (a) the fixed fields listed, including the executable base name; (b) yes/no values, pid and codes only. Party 3-1 for (a). Recommendation: (a).
5. **TLS on new spawns** (section 8). (a) file the ticket now; (b) not now. Party 4-0 for (a). Recommendation: (a).
6. **This design** (sections 1 to 8). Recommendation: accept.
