# finding-marvel-5x1s: a commit fails with "cannot run gpg" when the hook tools are off PATH

**Date:** 2026-11-10
**Confidence:** frontier. The trigger is observed; the source of the gpg wording is not.
**Placement:** marvel. The subject is marvel's lefthook pre-commit hook and the PATH it assumes; without that hook the friction does not arise. The seat environment and git's wording are objects. The alternative, the orc's `_kos/findings`, was rejected because no fleet-wide convention is the subject.
**Source:** builder seat, marvel#1393 (aae-orc-aeuo1), 2026-11-10. Two failed commit attempts, then one that landed.

## Symptom

`git commit` in a marvel worktree, from a shell whose PATH lacked `gofumpt` and `golangci-lint`, printed the hook output and then ended with, verbatim:

```
sh: golangci-lint: command not found
exit status 127
sh: gofumpt: command not found
error: cannot run gpg: No such file or directory
```

HEAD did not move on either of two attempts.

## What PATH lacked (commands run 2026-11-10)

- `command -v gpg ssh-keygen gofumpt golangci-lint` printed only `/usr/bin/ssh-keygen`. So gpg, gofumpt and golangci-lint were all absent.
- Signing does not need gpg here: `git config` shows `gpg.format=ssh`, `commit.gpgsign=true`, no `gpg.program`, no `gpg.ssh.program`, and the signer is `/usr/bin/ssh-keygen`, which was on PATH.
- In a scratch repo with the same PATH and no hook, `git commit --allow-empty` signed (`%G?` = G).
- Adding a pre-commit hook that runs `golangci-lint` made that scratch commit fail with `golangci-lint: command not found`, rc=1, and no gpg message.
- So the only missing thing that mattered was the two hook tools. The gpg wording was not reproduced in the scratch repo.

## Fix applied

Put `gofumpt` and `golangci-lint` on PATH for the commit command (the mise install directories), then re-ran the same `git commit -F`. It landed signed (`%G?` = G). No signing flag, config or program was changed or bypassed.

## Why

Unknown, needs probe. The lefthook `lint` command exits 127, which fails the hook, but the `cannot run gpg` line came from git after it, and the scratch repro (plain hook) did not print it. A probe would run the real lefthook hook with the tools off PATH in a throwaway clone and read where the gpg line comes from.

## Related

`lefthook.yml` pre-commit `fmt-check` ends in `|| true`, so a missing `gofumpt` prints an error but does not fail that command; only `lint` (and the exit 127) fails the hook.
