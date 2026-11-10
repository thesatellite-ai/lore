#!/usr/bin/env bash
# gate-parallel.sh: run independent gate steps at the same time, each with its own log and
# wall time, and fail naming every step that failed. Used by `task lore:check` (Taskfile.lore.yml).
#
#   tests/lib/gate-parallel.sh <stage> <name>=<command> [<name>=<command> ...]
#
# Every <command> runs under `bash -c` in the current folder; its stdout and stderr go to
# $GATE_LOG_DIR/<name>.log (default tmp/check). One line per step is printed as it ends, then
# the timing table of every step recorded so far in this gate run ($GATE_LOG_DIR/timings.tsv,
# one row per step: stage, step, status, seconds) and the wall time of every stage
# ($GATE_LOG_DIR/stages.tsv), so the table printed by the last stage covers the whole gate.
# A failed step prints the tail of its log.
#
# Why a script and not Taskfile `deps:`: deps run in parallel too, but interleave every step's
# output into one stream and print no per-step time, so neither "which step failed" nor "which
# step is slow" can be read from the result.
#
# Steps of one stage must be independent: no two may write the same file (two `go build -o`
# of one path, or two tasks with `deps: [build]` racing on tmp/bin/lore). Exit 1 when any step
# fails, 2 on a usage error. Portable to bash 3.2 (macOS): no `wait -n`, whole seconds only.

set -uo pipefail

# How many lines of a failed step's log are shown; the full log stays in $GATE_LOG_DIR.
FAIL_TAIL_LINES=40

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <stage> <name>=<command> [<name>=<command> ...]" >&2
  exit 2
fi
stage="$1"
shift

log_dir="${GATE_LOG_DIR:-tmp/check}"
mkdir -p "$log_dir"
timings="$log_dir/timings.tsv"
# One row per finished stage: stage, wall seconds of the whole stage (start of its first step
# to end of its last), so the total is real elapsed time, not a sum of parallel steps.
stages="$log_dir/stages.tsv"
stage_start=$SECONDS

names=()
pids=()
for spec in "$@"; do
  name="${spec%%=*}"
  cmd="${spec#*=}"
  if [ -z "$name" ] || [ "$name" = "$spec" ] || [ -z "$cmd" ]; then
    echo "gate-parallel: step must be <name>=<command>, got: $spec" >&2
    exit 2
  fi
  log="$log_dir/$name.log"
  (
    start=$SECONDS
    bash -c "$cmd" >"$log" 2>&1
    rc=$?
    secs=$((SECONDS - start))
    if [ "$rc" -eq 0 ]; then status=ok; mark="✓"; else status=FAIL; mark="✘"; fi
    printf '%s\t%s\t%s\t%s\n' "$stage" "$name" "$status" "$secs" >"$log.row"
    printf '  %s %-22s %5ss\n' "$mark" "$name" "$secs"
    exit "$rc"
  ) &
  names+=("$name")
  pids+=("$!")
  echo "▶ [$stage] $name  (log: $log)"
done

failed=()
for i in "${!pids[@]}"; do
  if ! wait "${pids[$i]}"; then failed+=("${names[$i]}"); fi
  cat "$log_dir/${names[$i]}.log.row" >>"$timings" 2>/dev/null
  rm -f "$log_dir/${names[$i]}.log.row"
done

printf '%s\t%s\n' "$stage" "$((SECONDS - stage_start))" >>"$stages"

for name in ${failed[@]+"${failed[@]}"}; do
  echo
  echo "──── $name FAILED: last $FAIL_TAIL_LINES lines of $log_dir/$name.log ────"
  tail -n "$FAIL_TAIL_LINES" "$log_dir/$name.log"
done

echo
echo "gate timings so far (wall seconds per step; steps of one stage ran in parallel):"
awk -F'\t' '{ printf "  %-8s %-22s %-5s %6ss\n", $1, $2, $3, $4 }' "$timings"
awk -F'\t' '
  { printf "  stage %-8s wall %6ss\n", $1, $2; total += $2 }
  END { printf "  total          %6ss\n", total }' "$stages"

if [ "${#failed[@]}" -gt 0 ]; then
  echo "✘ [$stage] failed: ${failed[*]}"
  exit 1
fi
echo "✓ [$stage] every step passed"
