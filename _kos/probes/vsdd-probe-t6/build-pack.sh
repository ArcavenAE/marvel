#!/bin/bash
# Build arm B's derived pack from the cosign-verified rc.25 tarball (brief
# sections 3 and 5.1). Nothing is published and nothing is installed; the
# output is an unpacked directory and a report.
#
#   build-pack.sh build TARBALL BUNDLE MANIFEST OUTDIR [N]
#   build-pack.sh check PACK_DIR MANIFEST
#   build-pack.sh report PACK_DIR MANIFEST VERSION
#
# build refuses unless the tarball and the manifest match the pins below and
# cosign verifies the tarball. The derived version is 1.0.0-rc.25.arm-b.N (N
# defaults to 1; each rebuild of the patch bumps it).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
TARBALL_SHA=6d38a905222bcf9de3b0fe74acd4fc29fbaca6d954f18c6ab3db27387831cee1
MANIFEST_SHA=e0ec70a15ebf74f409ef0b468b59bc400804157224383a62f526c8421c94325c
TOP=vsdd-factory-1.0.0-rc.25

die() { echo "build-pack: $*" >&2; exit 1; }
sha() { shasum -a 256 "$1" | cut -d' ' -f1; }

check() {
  [[ $# -eq 2 ]] || die "usage: build-pack.sh check PACK_DIR MANIFEST"
  python3 -I -B "$here/derive.py" check "$1" "$2"
}

report() {
  [[ $# -eq 3 ]] || die "usage: build-pack.sh report PACK_DIR MANIFEST VERSION"
  echo "derived pack $3"
  echo "tarball sha256 $TARBALL_SHA"
  echo "manifest sha256 $MANIFEST_SHA"
  echo "patch (apply_patch.py) sha256 $(sha "$here/apply_patch.py")"
  echo "gate (derive.py) sha256 $(sha "$here/derive.py")"
  echo "shim sha256 $(sha "$here/shim")"
  echo "manifest check:"
  check "$1" "$2"
}

build() {
  [[ $# -ge 4 && $# -le 5 ]] || die "usage: build-pack.sh build TARBALL BUNDLE MANIFEST OUTDIR [N]"
  local tarball="$1" bundle="$2" manifest="$3" out="$4" n="${5:-1}"
  [[ "$n" =~ ^[0-9]+$ ]] || die "N must be a number"
  local version="1.0.0-rc.25.arm-b.$n"
  [[ "$(sha "$tarball")" == "$TARBALL_SHA" ]] || die "tarball sha256 is not the pinned rc.25 tarball"
  [[ "$(sha "$manifest")" == "$MANIFEST_SHA" ]] || die "manifest sha256 is not the pinned rc.25 manifest"
  cosign verify-blob --bundle "$bundle" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity-regexp '^https://github.com/ArcavenAE/sideshow-packs/' "$tarball" \
    || die "cosign did not verify the tarball"
  [[ ! -e "$out" ]] || die "$out exists; build writes only into a new directory"
  mkdir -p "$out"
  tar -xzf "$tarball" -C "$out"
  local pack="$out/vsdd-factory-$version"
  mv "$out/$TOP" "$pack"
  chmod u+w "$pack/pack.yaml" "$pack/agents/orchestrator/orchestrator.md" "$pack/agents/orchestrator/per-story-delivery.md"
  python3 -I -B "$here/apply_patch.py" "$pack" >"$out/patched.txt"
  python3 -I -B "$here/derive.py" stamp "$pack/pack.yaml" "$version"
  cp "$here/shim" "$pack/bin/shim"
  chmod 0755 "$pack/bin/shim"
  report "$pack" "$manifest" "$version" >"$out/report.txt" || { cat "$out/report.txt" >&2; die "manifest check failed"; }
  cat "$out/report.txt"
  echo "pack directory: $pack"
}

case "${1:-}" in
  build) shift; build "$@" ;;
  check) shift; check "$@" ;;
  report) shift; report "$@" ;;
  *) die "usage: build-pack.sh build|check|report ..." ;;
esac
