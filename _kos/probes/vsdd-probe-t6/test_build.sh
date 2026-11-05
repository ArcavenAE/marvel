#!/bin/bash
# Tests for derive.py, apply_patch.py and build-pack.sh on small synthetic
# fixtures. No third-party text, no network, no cosign. Run:
#   bash _kos/probes/vsdd-probe-t6/test_build.sh
set -u
here="$(cd "$(dirname "$0")" && pwd)"
fails=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1: $2"; fails=$((fails + 1)); }
py() { python3 -I -B "$@"; }

mk_fixture() { # builds $T/pack and $T/manifest.csv like rc.25's, plus the derived state
  T=$(mktemp -d)
  mkdir -p "$T/pack/agents/orchestrator" "$T/pack/bin"
  echo "name: x"            >"$T/pack/pack.yaml"
  echo "version: 1.0.0-rc.25" >>"$T/pack/pack.yaml"
  echo "orch"               >"$T/pack/agents/orchestrator/orchestrator.md"
  echo "psd"                >"$T/pack/agents/orchestrator/per-story-delivery.md"
  echo "other"              >"$T/pack/bin/other"
  ( cd "$T/pack" && find . -type f | sed 's|^\./||' | sort | while read -r f; do
      printf '%s,%s,%s\n' "$(shasum -a 256 "$f" | cut -d' ' -f1)" "$(wc -c <"$f" | tr -d ' ')" "$f"
    done ) >"$T/manifest.csv"
}
derive_ok() { # apply the three allowed edits and the shim to $T/pack
  echo "orch patched" >"$T/pack/agents/orchestrator/orchestrator.md"
  echo "psd patched"  >"$T/pack/agents/orchestrator/per-story-delivery.md"
  py "$here/derive.py" stamp "$T/pack/pack.yaml" 1.0.0-rc.25.arm-b.1
  echo "#!/bin/bash" >"$T/pack/bin/shim"
}

# the allowed changes and the shim pass, and each changed path is listed with its sha256
mk_fixture; derive_ok
out=$(py "$here/derive.py" check "$T/pack" "$T/manifest.csv"); rc=$?
if [[ $rc -eq 0 && "$(echo "$out" | grep -c '^changed ')" == 3 && "$(echo "$out" | grep -c '^added ')" == 1 && "$out" == *"identical 1 of 4"* ]]; then pass "the allowed set passes and is listed with sha256"
else fail "the allowed set passes" "rc=$rc out=[$out]"; fi

# an unexpected change is refused
mk_fixture; derive_ok; echo tampered >"$T/pack/bin/other"
out=$(py "$here/derive.py" check "$T/pack" "$T/manifest.csv"); rc=$?
if [[ $rc -eq 1 && "$out" == *"unexpected-change bin/other"* ]]; then pass "an unexpected change is refused"
else fail "an unexpected change is refused" "rc=$rc out=[$out]"; fi

# a missing rc.25 path is refused
mk_fixture; derive_ok; rm "$T/pack/bin/other"
out=$(py "$here/derive.py" check "$T/pack" "$T/manifest.csv"); rc=$?
if [[ $rc -eq 1 && "$out" == *"missing bin/other"* ]]; then pass "a missing rc.25 path is refused"
else fail "a missing rc.25 path is refused" "rc=$rc out=[$out]"; fi

# an extra added path is refused
mk_fixture; derive_ok; echo x >"$T/pack/bin/extra"
out=$(py "$here/derive.py" check "$T/pack" "$T/manifest.csv"); rc=$?
if [[ $rc -eq 1 && "$out" == *"unexpected-add bin/extra"* ]]; then pass "an extra added path is refused"
else fail "an extra added path is refused" "rc=$rc out=[$out]"; fi

# a missing shim, or an allowed file left unpatched, is refused
mk_fixture; derive_ok; rm "$T/pack/bin/shim"
out=$(py "$here/derive.py" check "$T/pack" "$T/manifest.csv"); rc=$?
mk_fixture; derive_ok; cp "$T/pack/pack.yaml" "$T/p.yaml"; echo "version: 1.0.0-rc.25" >"$T/pack/pack.yaml"; echo "name: x" >"$T/pack/pack.yaml.tmp"; { echo "name: x"; echo "version: 1.0.0-rc.25"; } >"$T/pack/pack.yaml"
out2=$(py "$here/derive.py" check "$T/pack" "$T/manifest.csv"); rc2=$?
if [[ $rc -eq 1 && "$out" == *"expected-add-absent bin/shim"* && $rc2 -eq 1 && "$out2" == *"expected-change-absent pack.yaml"* ]]; then pass "a missing shim or an unpatched allowed file is refused"
else fail "a missing shim or unpatched file is refused" "rc=$rc out=[$out] rc2=$rc2 out2=[$out2]"; fi

# the version stamp changes the one line and nothing else
mk_fixture
cp "$T/pack/pack.yaml" "$T/before.yaml"
py "$here/derive.py" stamp "$T/pack/pack.yaml" 1.0.0-rc.25.arm-b.2
d=$(diff "$T/before.yaml" "$T/pack/pack.yaml" | grep -c '^[<>]')
if [[ "$d" == 2 && "$(grep -c 'version: 1.0.0-rc.25.arm-b.2' "$T/pack/pack.yaml")" == 1 && "$(grep -c '^name: x$' "$T/pack/pack.yaml")" == 1 ]]; then pass "the stamp changes the version line only"
else fail "the stamp changes the version line only" "diff lines=$d"; fi

# the stamp refuses a missing or repeated version line
mk_fixture; echo "name: x" >"$T/pack/pack.yaml"
py "$here/derive.py" stamp "$T/pack/pack.yaml" v >/dev/null 2>&1; r1=$?
printf 'version: 1.0.0-rc.25\nversion: 1.0.0-rc.25\n' >"$T/pack/pack.yaml"
py "$here/derive.py" stamp "$T/pack/pack.yaml" v >/dev/null 2>&1; r2=$?
if [[ $r1 -ne 0 && $r2 -ne 0 && "$(cat "$T/pack/pack.yaml" | wc -l | tr -d ' ')" == 2 ]]; then pass "the stamp refuses a missing or repeated version line"
else fail "the stamp refuses a missing or repeated version line" "r1=$r1 r2=$r2"; fi

# apply_patch refuses a target that is not the rc.25 file, and writes nothing
mk_fixture
cp "$T/pack/agents/orchestrator/orchestrator.md" "$T/o.before"
out=$(py "$here/apply_patch.py" "$T/pack" 2>&1); rc=$?
if [[ $rc -ne 0 && "$out" == *"is not the rc.25 file"* ]] && cmp -s "$T/o.before" "$T/pack/agents/orchestrator/orchestrator.md"; then pass "apply_patch refuses a non-rc.25 target and writes nothing"
else fail "apply_patch refuses a non-rc.25 target" "rc=$rc out=[$out]"; fi

# build refuses a tarball that is not the pinned one, before it writes anything
mk_fixture; echo junk >"$T/t.tgz"; echo b >"$T/t.bundle"
out=$("$here/build-pack.sh" build "$T/t.tgz" "$T/t.bundle" "$T/manifest.csv" "$T/out" 2>&1); rc=$?
if [[ $rc -ne 0 && "$out" == *"not the pinned rc.25 tarball"* && ! -e "$T/out" ]]; then pass "build refuses an unpinned tarball and writes nothing"
else fail "build refuses an unpinned tarball" "rc=$rc out=[$out]"; fi

# the shipped text carries no em dash
if grep -q $'\xe2\x80\x94' "$here"/apply_patch.py "$here"/derive.py "$here"/build-pack.sh "$here"/shim; then fail "no em dash in shipped text" "found one"
else pass "no em dash in the scripts or the inserted text"; fi

echo "failures: $fails"
[[ $fails -eq 0 ]]
