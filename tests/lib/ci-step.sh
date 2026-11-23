#!/usr/bin/env bash
# ci-step.sh: run one gate step on a GitHub Actions runner (.github/workflows/gate.yml) and record its
# verdict for the summary job.
#
#   tests/lib/ci-step.sh <step name> "<task and its vars>"     e.g. ci-step.sh userscen "test:scen PARALLEL=13"
#
# Runs `task --taskfile Taskfile.lore.yml <task> <vars>` with its output in tmp/check/<step>.log, then
# fails the step when it left a tracked or new untracked file changed (a gate step must leave the tree
# as it found it; the local gate's check:stamp refuses the same). Writes tmp/check/result-<step>.tsv:
# "<step> TAB PASS|FAIL TAB <wall seconds> TAB <note>", the note being the recall report's summary line.
set -uo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <step name> \"<task and its vars>\"" >&2
  exit 2
fi
step="$1"
spec="$2"
mkdir -p tmp/check
log="tmp/check/$step.log"
echo "step $step: task $spec (cores $(getconf _NPROCESSORS_ONLN), test time factor $(tests/lib/testtime.sh --factor), GATE_SLOTS ${GATE_SLOTS:-from the cores})"
start=$(date +%s)
# The local gate's build stage first (lore:build:test, tmp/bin/lore): the shell scenarios and chaos
# runs use that binary, and the local gate builds it once before any step starts.
task --taskfile Taskfile.lore.yml build:test >/dev/null 2>&1 || task --taskfile Taskfile.lore.yml build:test
# The task and its vars are words of the GATE_STEPS line (no quotes), split on purpose.
# shellcheck disable=SC2086
task --taskfile Taskfile.lore.yml $spec 2>&1 | tee "$log"
rc=${PIPESTATUS[0]}
wall=$(($(date +%s) - start))

dirty=$(git status --porcelain --untracked-files=all)
if [ -n "$dirty" ]; then
  echo "::error::step $step left the tree changed (a gate step must leave every tracked file as it was and add no untracked file):"
  echo "$dirty"
  git --no-pager diff --stat
  git --no-pager diff | head -300
  rc=1
fi

note=""
if [ "$step" = "recall" ]; then
  note=$(grep -o 'summary: .*' "$log" | tail -1 | tr '\t' ' ')
fi
verdict=PASS
[ "$rc" -eq 0 ] || verdict=FAIL
printf '%s\t%s\t%s\t%s\n' "$step" "$verdict" "$wall" "$note" >"tmp/check/result-$step.tsv"
echo "step $step: $verdict in ${wall}s"
exit "$rc"
