#!/usr/bin/env bash
# Fail when the alpha tag does not point at the commit the binaries were built
# from. gh release create tags the commit it is given with --target; this reads
# the tag back and compares, so a tag that landed elsewhere fails the run
# instead of waiting for someone to notice (marvel#1015). It only reads. On a
# mismatch it deletes nothing and says which tag, where it points and what was
# expected, so a person can act from the log alone.
#
#   scripts/check-release-tag.sh <tag> <commit-sha>
#
# Needs GITHUB_REPOSITORY and a GH_TOKEN that gh can use. A lightweight tag
# points at a commit; an annotated tag points at a tag object, which is peeled
# until it reaches a commit. Exit 0 when the commit matches, 1 on a mismatch or
# a lookup that fails, 2 on a usage error.
set -euo pipefail

tag="${1:-}"
want="${2:-}"
[[ -n "$tag" && -n "$want" && $# -eq 2 ]] || { echo "usage: check-release-tag.sh <tag> <commit-sha>" >&2; exit 2; }
[[ -n "${GITHUB_REPOSITORY:-}" ]] || { echo "check-release-tag: GITHUB_REPOSITORY is not set" >&2; exit 2; }

api="repos/${GITHUB_REPOSITORY}/git"
read -r type sha < <(gh api "${api}/ref/tags/${tag}" --jq '.object.type + " " + .object.sha')
# Annotated tags can nest; stop after a few levels rather than loop on bad data.
for _ in 1 2 3 4 5; do
  [[ "$type" == tag ]] || break
  read -r type sha < <(gh api "${api}/tags/${sha}" --jq '.object.type + " " + .object.sha')
done

if [[ "$type" != commit || "$sha" != "$want" ]]; then
  echo "release tag ${tag} points at ${type} ${sha}, but the binaries were built from commit ${want}" >&2
  echo "nothing was deleted; the tag and the release are as published" >&2
  exit 1
fi
echo "release tag ${tag} points at the built commit ${want}"
