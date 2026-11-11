#!/usr/bin/env bash
# run-scripts.sh: run shell test scripts (tests/scenarios/SC-*.sh, tests/chaos/CH-*.sh) at the
# same time, JOBS at a time, and fail naming every script that failed.
#
#   JOBS=8 tests/lib/run-scripts.sh <label> <script>...
#
# Every script already isolates itself (tests/lib/common.sh: mk_tmp gives it its own temp
# folder, HOME and git config, and the hermetic PATH shim is its own mktemp folder), so two
# scripts never share a file and the order they run in changes nothing they check. Each
# script's output goes to its own log, printed in the order given once all have ended, with
# the same "▶ name / ✓ PASS / ✗ FAIL" lines a serial run printed.
#
# Longest first: when $DURATIONS_DIR/<label>.tsv (written by the previous run; tmp/, never
# tracked) lists how long each script took, the slowest start first, so a long script (CH-1
# seeds 200 rows one command at a time) never starts last and leaves the other slots idle.
# Without it the scripts start in the order given.
#
# Why: run one after another, the 30 scenarios and the 12 chaos scripts took 116 s and 100 s
# on an idle machine while each used about one core.
set -uo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <label> <script>..." >&2
  exit 2
fi
label="$1"
shift
# 8: each script keeps about one core busy; the gate runs this beside other suites under its
# load budget (docs/DEVELOPMENT.md "The gate").
jobs="${JOBS:-8}"
# tmp/, not the gate log folder: lore:check empties tmp/check at its start.
durations_dir="${DURATIONS_DIR:-tmp}"
durations="$durations_dir/$label-durations.tsv"
logs=$(mktemp -d)
trap 'rm -rf "$logs"' EXIT

# seconds<TAB>script, slowest first (unknown scripts count as 0 and keep their given order).
order() {
  local f secs
  for f in "$@"; do
    secs=0
    if [ -f "$durations" ]; then
      secs=$(awk -F'\t' -v n="$(basename "$f")" '$1 == n { print $2; exit }' "$durations")
    fi
    printf '%s\t%s\n' "${secs:-0}" "$f"
  done | sort -t "$(printf '\t')" -s -k1,1nr | cut -f2
}

order "$@" | xargs -P "$jobs" -I{} bash -c '
  start=$SECONDS
  bash "$1" >"$2/$(basename "$1").log" 2>&1
  rc=$?
  printf "%s %s\n" "$rc" "$((SECONDS - start))" >"$2/$(basename "$1").rc"' _ {} "$logs"

failed=()
: >"$logs/durations.tsv"
for f in "$@"; do
  name=$(basename "$f")
  rc=1 secs=0
  [ -f "$logs/$name.rc" ] && read -r rc secs <"$logs/$name.rc"
  printf '%s\t%s\n' "$name" "$secs" >>"$logs/durations.tsv"
  echo "▶ $name (${secs}s)"
  cat "$logs/$name.log" 2>/dev/null
  if [ "$rc" = 0 ]; then
    echo "  ✓ PASS"
  else
    echo "  ✗ FAIL"
    failed+=("$name")
  fi
done
mkdir -p "$durations_dir" && cp "$logs/durations.tsv" "$durations"

if [ "${#failed[@]}" -gt 0 ]; then
  echo "${#failed[@]} $label script(s) FAILED: ${failed[*]}"
  exit 1
fi
echo "All $label scripts PASSED"
