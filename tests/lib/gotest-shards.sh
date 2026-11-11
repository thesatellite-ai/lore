#!/usr/bin/env bash
# gotest-shards.sh: run the tests of ONE Go package as several processes at once, so a package
# whose tests cannot all run in parallel inside one process (t.Setenv, os.Chdir, process-wide
# state) still uses more than a few cores.
#
#   tests/lib/gotest-shards.sh <shards> <package> [go test flags...]
#
# The package's test binary is built once (go test -c with the given flags), its top-level tests
# are listed (-test.list) and dealt into <shards> groups, and each group runs as its own process
# of that binary (-test.run '^(A|B|…)$') in the package folder, as go test would run it. Every
# test runs exactly once; a process has its own environment and working folder, so the state a
# serial test changes is never seen by a test of another shard. Exit 1 when any shard fails
# (its whole output is printed), 2 on a usage error.
#
# Balance: when $SHARD_DURATIONS (default tmp/shards-<package path with _>.tsv, written by the
# previous run) has each test's seconds, tests are dealt slowest first to the shard with the
# least time so far; otherwise round robin by name. The file is only a hint: a new or renamed
# test is still run.
#
# Flags: go test build flags (-race, -gcflags=…, -tags …) go to the build; -timeout and -v go
# to every shard's run. Why a script: `go test -race ./wire` took 200 s of a 248 s unit stage
# with 123 of its 569 tests serial (113 use t.Setenv), while every other package of the race
# set finished in under 25 s.
set -uo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <shards> <package> [go test flags...]" >&2
  exit 2
fi
shards="$1"
pkg="$2"
shift 2

build_flags=()
run_flags=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    -timeout) run_flags+=("-test.timeout=$2"); shift 2 ;;
    -timeout=*) run_flags+=("-test.timeout=${1#-timeout=}"); shift ;;
    -v) run_flags+=("-test.v"); shift ;;
    -tags | -gcflags | -ldflags) build_flags+=("$1" "$2"); shift 2 ;;
    *) build_flags+=("$1"); shift ;;
  esac
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
dir=$(go list -f '{{.Dir}}' "$pkg") || exit 1
bin="$work/pkg.test"
go test -c -o "$bin" ${build_flags[@]+"${build_flags[@]}"} "$pkg" || exit 1

(cd "$dir" && "$bin" -test.list '.*') | grep -E '^(Test|Example|Fuzz)' >"$work/tests" || true
if [ ! -s "$work/tests" ]; then
  echo "gotest-shards: $pkg has no tests" >&2
  exit 1
fi

key=$(printf '%s' "$pkg" | tr -c 'A-Za-z0-9' '_')
durations="${SHARD_DURATIONS:-tmp/shards-$key.tsv}"
if [ -f "$durations" ]; then cp "$durations" "$work/known"; else : >"$work/known"; fi
# test<TAB>shard: slowest first onto the least loaded shard (known seconds, else 1 each; +0.05 so
# tests that took no measurable time still spread over the shards instead of all landing on one).
# The durations file is read by name, not as "the first file": an empty or missing one must
# not swallow the test list (awk's NR == FNR is true for the second file then).
awk -F'\t' -v n="$shards" -v durfile="$work/known" '
  FILENAME == durfile { secs[$1] = $2; next }
  { name[++c] = $1; s[c] = (($1 in secs) ? secs[$1] + 0 : 1) + 0.05 }
  END {
    for (i = 1; i <= c; i++) order[i] = i
    for (i = 2; i <= c; i++) { j = i; while (j > 1 && s[order[j-1]] < s[order[j]]) { t = order[j]; order[j] = order[j-1]; order[j-1] = t; j-- } }
    for (k = 0; k < n; k++) load[k] = 0
    for (i = 1; i <= c; i++) {
      best = 0
      for (k = 1; k < n; k++) if (load[k] < load[best]) best = k
      load[best] += s[order[i]]
      print name[order[i]] "\t" best
    }
  }' "$work/known" "$work/tests" >"$work/plan"

pids=()
for ((k = 0; k < shards; k++)); do
  names=$(awk -F'\t' -v k="$k" '$2 == k { print $1 }' "$work/plan" | paste -sd'|' -)
  [ -n "$names" ] || continue
  (
    cd "$dir" && "$bin" -test.run "^($names)\$" -test.v ${run_flags[@]+"${run_flags[@]}"} >"$work/shard-$k.log" 2>&1
  ) &
  pids+=("$!:$k")
done

failed=()
for pk in "${pids[@]}"; do
  if ! wait "${pk%%:*}"; then failed+=("${pk##*:}"); fi
done

# Each test's seconds for the next run's balance (top-level "--- PASS/FAIL: Name (1.23s)" lines).
cat "$work"/shard-*.log | sed -nE 's/^--- (PASS|FAIL|SKIP): ([^ ]+) \(([0-9.]+)s\)$/\2\t\3/p' >"$work/durations"
mkdir -p "$(dirname "$durations")" && cp "$work/durations" "$durations"

total=$(wc -l <"$work/tests" | tr -d ' ')
# Every listed test must have reported a result: a plan or a -test.run pattern that drops a
# test would otherwise pass while testing less.
planned=$(cut -f1 "$work/plan" | sort -u | wc -l | tr -d ' ')
ran=$(cut -f1 "$work/durations" | sort -u | wc -l | tr -d ' ')
if [ "$planned" != "$total" ] || [ "$ran" != "$total" ]; then
  echo "gotest-shards: $pkg lists $total tests, $planned were planned and $ran reported a result:" >&2
  cut -f1 "$work/durations" | sort -u | comm -23 <(sort -u "$work/tests") - | sed 's/^/  did not run: /' >&2
  for k in $(seq 0 $((shards - 1))); do [ -f "$work/shard-$k.log" ] && tail -n 20 "$work/shard-$k.log"; done
  exit 1
fi
for k in ${failed[@]+"${failed[@]}"}; do
  echo "──── shard $k of $pkg FAILED ────"
  cat "$work/shard-$k.log"
done
if [ "${#failed[@]}" -gt 0 ]; then
  echo "FAIL	$pkg	($total tests in $shards shards; failed shards: ${failed[*]})"
  exit 1
fi
# The -v output of passing shards is kept short: the slowest tests, then the go test-style line.
sort -t "$(printf '\t')" -k2,2nr "$work/durations" | head -5 | awk -F'\t' '{ printf "  slowest: %s %ss\n", $1, $2 }'
echo "ok  	$pkg	($total tests in $shards shards)"
