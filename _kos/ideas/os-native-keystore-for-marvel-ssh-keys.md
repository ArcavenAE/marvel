# OS-native keystore for marvel's SSH keys (macOS and Linux)

Pre-hypothesis capture. Generative, no commitment. Requested by the
director (2026-09-16). The point is to name a research question and its
crux constraints, not to pick a backend.

## The idea

Marvel holds an SSH client keypair for the `mrvl://` transport. Today it
sits on disk as files under `~/.marvel/keys` (`internal/keys`), protected
by nothing stronger than filesystem permissions. The idea: let marvel keep
those keys in an OS-native secret store instead, so the private key is not a
plain file that any process running as the same user can read.

macOS is the easy side: the native Keychain already exists, marvel already
depends on a keychain-capable security session for harness auth (finding-041),
and the login keychain is the obvious home for a marvel key item.

Linux is the whole research question, and it is where this idea earns a
probe rather than a design.

## The crux (record these as hard constraints, not preferences)

- **No distro-specific requirement.** A backend that works on one
  distribution and not another fails the point. Marvel is one Go binary that
  should behave the same across the hosts a fleet runs on.
- **No dependency on X or a desktop session.** Specifically, no hard
  dependency on GNOME Keyring, the Secret Service D-Bus API, or KWallet.
  Marvel runs headless on servers. A secret store that assumes a logged-in
  desktop, a running D-Bus session bus, or a graphical unlock prompt is a
  non-starter for the Linux side. This is the constraint that rules out the
  path most language keyring libraries take by default, so it is the first
  thing any candidate must clear.
- **A floor that always works.** If no richer store is present, marvel
  should still function with file-based keys behind strict OS permissions
  (the current baseline). The keystore is an upgrade over that floor, never a
  new precondition for running at all.

## Candidates to explore (not conclusions)

Each needs a headless-server reality check against the constraints above.

- **Kernel keyring / `keyctl`.** In-kernel, no desktop, no D-Bus. Questions:
  persistence across reboot and across the daemon's own restart/reexec
  (session vs user vs persistent keyrings), and whether a detached-tmux or
  launchd/systemd-parented daemon lands in a keyring it can read back (the
  Linux cousin of the macOS security-session problem in finding-041).
- **TPM-backed storage.** Hardware root of trust, present on much modern
  server hardware. Questions: availability floor across the fleet's actual
  hosts (VMs, older boxes), the userspace stack required, and whether it
  raises the same "which security context can unseal it" question.
- **`pass` (GnuPG + files).** Headless-friendly, scriptable, no desktop.
  Questions: it moves custody to a GPG key, so where does *that* key live,
  and does that just relocate the problem.
- **File-based with OS permissions as the floor.** What marvel does now.
  Worth stating explicitly as the baseline every other candidate is measured
  against, and as the guaranteed fallback.

Sibling fleet CLIs (stave, bloomctl, sidestep) already store secrets via the
Rust `keyring` crate (bd `aae-orc-ydto`, `aae-orc-qpa49`, `aae-orc-vch2t`,
`aae-orc-zb5ar`). That is prior art, but with a caveat directly on point:
`keyring` on Linux resolves to Secret Service over D-Bus by default, which is
the desktop dependency this idea rules out. So the sibling pattern is a lead
for macOS and a cautionary example for headless Linux, not a ready answer.
Marvel is Go, not Rust, which is a second reason the crate is a reference
rather than a drop-in.

## Shape hypothesis: a pluggable backend, maybe marvel's first addon

The backends above are not one thing. Their availability is per-host, and
their trade-offs differ enough that baking one into core looks wrong. So the
shape hypothesis is that key storage wants a **backend interface** with
per-host selection (detect what the host offers, fall back to the file
floor), rather than a single hardcoded store.

That interface is small, but it is the thin end of a larger question: does
marvel want an **addon / plugin / module system** at all, so host-specific
capabilities like a keystore backend live outside core and are selected per
host? If the answer is yes, key storage is a good first customer for it,
because it has a clean interface, a guaranteed fallback, and a real
per-host-capability reason to be pluggable. If the answer is no, the backend
interface stays an internal seam. Either way, the pluggability question is
the interesting part, and it is bigger than keys alone.

## Prior art and related work

- **marvel finding-041** (the daemon must start in a keychain-capable macOS
  session): the closest neighbor. It establishes that macOS gates secret
  access by security session, not by environment or `HOME`, which is exactly
  the class of problem a Linux keystore backend has to answer for its own
  context. Read it before designing anything here.
- **marvel finding-025** (home isolation breaks harness auth): the related
  `HOME`-vs-security-context distinction on macOS.
- **bd `aae-orc-kim3`** (marvel security assessment: listeners, cluster
  network path, sockets, ssh key handling and storage, before a second user
  runs it): this idea is one input to that assessment's key-handling section.
- **akey / YubiKey SSH-agent context**: the fleet already routes SSH signing
  through a hardware-resident key via an agent proxy (keys never touch disk;
  the YubiKey is the custody boundary). A hardware-backed agent is a fifth
  candidate shape and a useful contrast: it keeps the private key off the
  host entirely rather than storing it better on the host.

## Open questions

- Does the daemon's own restart/reexec survive whatever store is chosen, the
  way the managed-bus credentials survive a reexec today?
- Is the unit of storage the private key itself, or a passphrase that unlocks
  an on-disk key, and does that choice change the constraint analysis?
- Where does this sit against the ADR-009 custody boundary: a marvel SSH
  client key is authority marvel itself mints and can re-mint, so it is
  issuance, not third-party custody, which suggests storing it better is in
  scope rather than out of it.
- Is per-host backend detection worth the complexity, or does the file floor
  plus macOS Keychain cover enough of the real fleet to defer the Linux
  backends entirely.
