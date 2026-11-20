#!/usr/bin/env bash
# test-times.sh: record how long every top-level test of an end-to-end suite took, from a
# `go test -json` run of it, into the gate's budget table (tests/lib/gate-test-times.tsv).
#
#   go test -json -tags journeys -count=1 -parallel 2 ./tests/journeys/ > run.json
#   tests/lib/test-times.sh tests/journeys run.json
#
# Why: the gate runs each suite in parts (GATE_STEPS in Taskfile.lore.yml), each part under one
# go test -timeout. A part that grew past its limit died with tests never started, and nobody
# saw it until those tests had not run for a day (devco D-779). internal/repoguard
# TestGateStepsFitTheirTimeout reads this table, adds up each part's tests at the part's
# -parallel and fails when a part needs more than 70 % of its timeout; a test missing from the
# table counts as the mean of its suite, so a new test is in the sum the day it is added.
#
# Columns: suite folder, test name, wall seconds (go test's Elapsed for the test, so the time it
# ran, not the time it waited for a parallel slot), and "parallel" when the test called
# t.Parallel() (go test -json shows a pause for it) or "serial" when it did not. The rows of the
# named suite are replaced; the rows of every other suite are kept. A test that did not finish
# (timeout, crash) has no Elapsed and keeps its old row. Run it on the gate server with the
# load of a gate when you can: the guard's 70 % leaves room for a slower day, not for a
# measurement taken on an idle machine.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <suite folder, e.g. tests/journeys> <go test -json output file>" >&2
  exit 2
fi
suite="${1%/}"
json="$2"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
table="${GATE_TEST_TIMES:-$here/gate-test-times.tsv}"
[ -r "$json" ] || { echo "test-times: cannot read $json" >&2; exit 2; }

new="$(mktemp)"
trap 'rm -f "$new"' EXIT
# go test -json writes one object per line with the keys in a fixed order; a top-level test has
# no "/" in its name. Pull the three fields with plain matching (no jq on the gate server).
awk -v suite="$suite" '
  function field(name,   re, s) {
    re = "\"" name "\":\"[^\"]*\""
    if (!match($0, re)) return ""
    s = substr($0, RSTART, RLENGTH)
    sub("^\"" name "\":\"", "", s); sub("\"$", "", s)
    return s
  }
  {
    test = field("Test"); if (test == "" || test ~ /\//) next
    action = field("Action")
    if (action == "pause") par[test] = 1
    if (action == "pass" || action == "fail" || action == "skip") {
      if (!match($0, /"Elapsed":[0-9.]+/)) next
      el = substr($0, RSTART + 10, RLENGTH - 10)
      secs[test] = el
    }
  }
  END {
    for (t in secs) printf "%s\t%s\t%.1f\t%s\n", suite, t, secs[t], (t in par) ? "parallel" : "serial"
  }' "$json" >"$new"

if [ ! -s "$new" ]; then
  echo "test-times: no finished top-level test in $json (was it go test -json?)" >&2
  exit 1
fi
count=$(wc -l <"$new")

# Keep the header, the other suites' rows and this suite's rows for tests that did not finish
# in this run; take every finished test from the new run. Sorted, so the diff of a re-measure
# shows only what moved.
{
  echo "# suite	test	seconds	mode   (written by tests/lib/test-times.sh; read by internal/repoguard TestGateStepsFitTheirTimeout)"
  {
    [ -f "$table" ] && awk -F'\t' -v suite="$suite" -v newf="$new" '
      BEGIN { while ((getline l < newf) > 0) { split(l, f, "\t"); fresh[f[2]] = 1 } }
      /^#/ || NF < 4 { next }
      $1 == suite && ($2 in fresh) { next }
      { print }' "$table"
    cat "$new"
  } | sort -t$'\t' -k1,1 -k2,2
} >"$table.tmp"
mv "$table.tmp" "$table"
echo "test-times: $count tests of $suite recorded in $table"
