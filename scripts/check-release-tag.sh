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
# a lookup that fails or returns nothing usable, 2 on a usage error.
set -euo pipefail

tag="${1:-}"
want="${2:-}"
[[ -n "$tag" && -n "$want" && $# -eq 2 ]] || { echo "usage: check-release-tag.sh <tag> <commit-sha>" >&2; exit 2; }
[[ -n "${GITHUB_REPOSITORY:-}" ]] || { echo "check-release-tag: GITHUB_REPOSITORY is not set" >&2; exit 2; }

api="repos/${GITHUB_REPOSITORY}/git"

# One line naming the tag and the commit the run built, so the log alone says
# what failed and what was expected, whatever gh printed on its own.
cannot_read() {
  echo "release tag ${tag}: could not read ${1}; the binaries were built from commit ${want}" >&2
  echo "nothing was deleted; the tag and the release are as published" >&2
  exit 1
}

# read_object <api path> <what>: set type and sha from one gh api read. A gh
# that fails, whatever it printed, or one that prints anything but "<type>
# <sha>", is a failed read.
read_object() {
  local out pair='^[^[:space:]]+ [^[:space:]]+$'
  out="$(gh api "$1" --jq '.object.type + " " + .object.sha')" || cannot_read "$2"
  [[ "$out" =~ $pair ]] || cannot_read "$2"
  type="${out%% *}"
  sha="${out#* }"
}

read_object "${api}/ref/tags/${tag}" "the tag ref"
# Annotated tags can nest; stop after a few levels rather than loop on bad data.
for _ in 1 2 3 4 5; do
  [[ "$type" == tag ]] || break
  read_object "${api}/tags/${sha}" "the tag object ${sha}"
done

if [[ "$type" != commit || "$sha" != "$want" ]]; then
  echo "release tag ${tag} points at ${type} ${sha}, but the binaries were built from commit ${want}" >&2
  echo "nothing was deleted; the tag and the release are as published" >&2
  exit 1
fi
echo "release tag ${tag} points at the built commit ${want}"
