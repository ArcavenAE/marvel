# marvel: an scp-like verified file channel (RBAC-gated, owner-controlled)

Status: idea (pre-hypothesis). Subject: marvel. Filed 2026-09-21.

## The gap this answers

Operating the fleet turned up a concrete wall: there is no clean way to move
a checksum-verified file between two hosts marvel already federates. In the
ops-manifest incident (2026-09-21) an agent on skippy had the authoritative
~15KB manifest in its scratchpad, and a director on kinu needed those exact
bytes to run `marvel work` from kinu. Every channel failed:

- no shared filesystem between the hosts;
- host SSH refused (only `mrvl://` was open), so scp/rsync were unavailable;
- the tmux inject/capture channel truncates well under 15KB (the stagekeeper
  launch-prompt truncation finding is the same failure mode);
- git was ruled out because the manifest carried host-local paths and the
  target repo is public (a correct redaction call).

marvel already holds an authenticated, RBAC-capable transport between hosts
(the `mrvl://` control plane). It is the natural carrier for a small, verified
file transfer, the way `kubectl cp` rides the kube API. Captured as director
sim O-26 (no clean cross-host verified-artifact transfer).

## The shape

- `marvel cp <src> <cluster>:<dst>` (and the reverse), riding `mrvl://`.
- Checksum-verified end to end: the transfer carries a digest and the
  receiver refuses a mismatch, so a truncated or altered copy fails closed
  rather than landing a corrupt artifact. (This is what let the ops-manifest
  recipe be safe even by hand: sha in, sha out.)
- Small-artifact scope by default (manifests, configs, keys-material handled
  by the credential plane, not this), not a general bulk mover.

## Gating and owner control (load-bearing, same stance as the mrvl-proxy idea)

- RBAC-gated per direction and per path prefix: who may push, who may pull,
  into which directories on which cluster.
- First-class control for the system owner / installer, external to the
  marvel control plane, mirroring the mrvl-proxy idea: the owner can disable
  the file channel globally, or apply policy (read-only pull, no push,
  allow only whitelisted path prefixes). Without an owner-external off switch
  and policy ceiling, many operators will not enable a control plane that can
  write files onto their hosts at all.
- Audit: every transfer logged (who, what digest, which path, which cluster).

## Complement, not a substitute

A manifest-export verb (`marvel get/describe -o yaml`, the `kubectl get -o yaml`
analog) is the other half and is separately worth building: it would have let
the director pull the live desired state from the daemon over `mrvl://`
without any file transfer at all. `marvel cp` is the general case; `-o yaml`
is the common case for the specific artifact marvel already owns.

## Related

- [[mrvl-proxy-nats-and-generic-relays]] (same owner-external-control stance).
- director sim O-26 (the transfer-gap observation) and the 2026-09-21
  ops-manifest incident.
- Bears on the credential-custody boundary (ADR-009): key material is NOT
  this channel's job; it brokers scoped credentials, it does not scp secrets.
