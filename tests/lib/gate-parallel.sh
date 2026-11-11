#!/usr/bin/env bash
# gate-parallel.sh: run independent gate steps at the same time, each with its own log and
# wall time, and fail naming every step that failed. Used by `task lore:check` (Taskfile.lore.yml).
#
#   tests/lib/gate-parallel.sh <stage> <name>[:<cores>]=<command> [...]
#
# Load budget: with GATE_SLOTS=<n> set, a step that names its <cores> (about how many cores it
# keeps busy: its -parallel, JOBS or shard count) starts only while the cores of the steps
# already running plus its own stay within n, in the order the steps are given (so list the
# longest first; a step that does not fit holds back the ones after it); a step without
# <cores> counts as 1, and a step larger than n still starts once nothing else runs. Without GATE_SLOTS every step starts at once (the old behaviour).
# Why: the e2e stage started all 21 suites at once on a 48-core machine; the load averaged 161
# (peak 308), 42 % of the CPU went to the kernel switching between processes, and the stage
# took 1276 s for work that needs far less when it is fed to the cores at the rate they take it.
#
# Every <command> runs under `bash -c` in the current folder; its stdout and stderr go to
# $GATE_LOG_DIR/<name>.log (default tmp/check). One line per step is printed as it ends, then
# the timing table of every step recorded so far in this gate run ($GATE_LOG_DIR/timings.tsv,
# one row per step: stage, step, status, wall seconds, CPU seconds (user+sys of the step and
# every process it waited for)) and the wall time and load of every stage
# ($GATE_LOG_DIR/stages.tsv: stage, wall seconds, peak and mean 1-minute load average sampled
# every LOAD_SAMPLE_SECS), so the table printed by the last stage covers the whole gate. The CPU
# column is what a step costs the machine: the gate's wall time is bound by the sum of the CPU
# of the steps that run together divided by the cores, not by the slowest step alone.
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
# How often the load average is sampled while a stage runs (seconds).
LOAD_SAMPLE_SECS=10

# load1 prints the 1-minute load average (Linux /proc, macOS sysctl), or nothing.
load1() {
  if [ -r /proc/loadavg ]; then
    cut -d' ' -f1 /proc/loadavg
  else
    sysctl -n vm.loadavg 2>/dev/null | tr -d '{}' | awk '{print $1}'
  fi
}

# cpu_secs turns the children line of bash's `times` ("0m12.340s 0m1.200s") into whole seconds.
cpu_secs() {
  awk '{ s = 0; for (i = 1; i <= 2; i++) { split($i, a, /[ms]/); s += a[1] * 60 + a[2] } printf "%d\n", s + 0.5 }'
}

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
load_file="$log_dir/load-$stage.txt"
: >"$load_file"
(
  while :; do load1 >>"$load_file"; sleep "$LOAD_SAMPLE_SECS"; done
) &
sampler=$!

# slots: the load budget (0 = no budget, every step at once).
slots="${GATE_SLOTS:-0}"
names=()
cmds=()
cores=()
for spec in "$@"; do
  head="${spec%%=*}"
  cmd="${spec#*=}"
  name="${head%%:*}"
  want=1
  [ "$name" != "$head" ] && want="${head#*:}"
  case "$want" in '' | *[!0-9]*) want=x ;; esac
  if [ -z "$name" ] || [ "$head" = "$spec" ] || [ -z "$cmd" ] || [ "$want" = x ]; then
    echo "gate-parallel: step must be <name>[:<cores>]=<command>, got: $spec" >&2
    exit 2
  fi
  names+=("$name")
  cmds+=("$cmd")
  cores+=("$want")
done

# start_step I: run step I in the background; its pid goes to pid_of[I].
pid_of=()
start_step() {
  local i="$1" name="${names[$1]}" cmd="${cmds[$1]}"
  local log="$log_dir/$name.log"
  (
    start=$SECONDS
    bash -c "$cmd" >"$log" 2>&1
    rc=$?
    secs=$((SECONDS - start))
    # `times` line 2: user and sys time of every child this subshell waited for (the step).
    # Redirected to a file, not piped: in a pipe `times` runs in a new subshell that has no children.
    times >"$log.times"
    cpu=$(sed -n 2p "$log.times" | cpu_secs)
    rm -f "$log.times"
    if [ "$rc" -eq 0 ]; then status=ok; mark="✓"; else status=FAIL; mark="✘"; fi
    printf '%s\t%s\t%s\t%s\t%s\n' "$stage" "$name" "$status" "$secs" "$cpu" >"$log.row"
    printf '  %s %-22s %5ss  cpu %6ss\n' "$mark" "$name" "$secs" "$cpu"
    exit "$rc"
  ) &
  pid_of[$i]=$!
  echo "▶ [$stage] $name  (log: $log)"
}

failed=()
# finish_step I: collect step I's exit status and timing row.
finish_step() {
  local i="$1"
  if ! wait "${pid_of[$i]}"; then failed+=("${names[$i]}"); fi
  cat "$log_dir/${names[$i]}.log.row" >>"$timings" 2>/dev/null
  rm -f "$log_dir/${names[$i]}.log.row"
}

pending=()  # indexes of the steps not started yet, in the order given
for ((i = 0; i < ${#names[@]}; i++)); do pending+=("$i"); done
used=0      # cores of the running steps
running=()  # indexes of the running steps
while [ "${#pending[@]}" -gt 0 ] || [ "${#running[@]}" -gt 0 ]; do
  # Start pending steps in the order given while they fit the budget. The first that does not
  # fit waits for cores, and so does every step after it: letting smaller steps past it could
  # keep it waiting until they are all gone (the list is longest first, so it must start early).
  left=()
  for i in ${pending[@]+"${pending[@]}"}; do
    w="${cores[$i]}"
    if [ "${#left[@]}" -gt 0 ] || { [ "$slots" -gt 0 ] && [ "${#running[@]}" -gt 0 ] && [ $((used + w)) -gt "$slots" ]; }; then
      left+=("$i")
      continue
    fi
    start_step "$i"
    running+=("$i")
    used=$((used + w))
  done
  pending=(${left[@]+"${left[@]}"})
  [ "${#running[@]}" -eq 0 ] && break
  if [ "${#pending[@]}" -eq 0 ]; then
    # Nothing left to schedule: wait for the rest in order.
    for i in "${running[@]}"; do finish_step "$i"; done
    running=()
    break
  fi
  sleep 1
  still=()
  for i in "${running[@]}"; do
    if kill -0 "${pid_of[$i]}" 2>/dev/null; then
      still+=("$i")
    else
      finish_step "$i"
      used=$((used - cores[i]))
    fi
  done
  running=(${still[@]+"${still[@]}"})
done

kill "$sampler" 2>/dev/null
wait "$sampler" 2>/dev/null
load1 >>"$load_file"
load_stats=$(awk '$1 != "" { n++; t += $1; if ($1 > m) m = $1 } END { if (n) printf "%.1f\t%.1f", m, t / n; else printf "-\t-" }' "$load_file")
printf '%s\t%s\t%s\n' "$stage" "$((SECONDS - stage_start))" "$load_stats" >>"$stages"

for name in ${failed[@]+"${failed[@]}"}; do
  echo
  echo "──── $name FAILED: last $FAIL_TAIL_LINES lines of $log_dir/$name.log ────"
  tail -n "$FAIL_TAIL_LINES" "$log_dir/$name.log"
done

echo
echo "gate timings so far (wall seconds per step; steps of one stage ran in parallel):"
awk -F'\t' '{ printf "  %-8s %-22s %-5s %6ss  cpu %6ss\n", $1, $2, $3, $4, $5 }' "$timings"
awk -F'\t' '
  { printf "  stage %-8s wall %6ss  load peak %5s mean %5s\n", $1, $2, $3, $4; total += $2 }
  END { printf "  total          %6ss\n", total }' "$stages"

if [ "${#failed[@]}" -gt 0 ]; then
  echo "✘ [$stage] failed: ${failed[*]}"
  exit 1
fi
echo "✓ [$stage] every step passed"
