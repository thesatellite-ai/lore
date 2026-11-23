#!/usr/bin/env bash
# gate-matrix.sh: print GATE_STEPS of Taskfile.lore.yml as the job matrix of the Actions gate
# (.github/workflows/gate.yml), one JSON object per step: {"step": <name>, "task": <task and vars>}.
#
#   tests/lib/gate-matrix.sh [Taskfile.lore.yml]   →   {"include":[{"step":"userscen","task":"test:scen PARALLEL=13"},...]}
#
# Why: the gate's steps are listed once, in GATE_STEPS; the workflow reads them from here, so a
# step added to GATE_STEPS runs on Actions with no workflow edit (internal/repoguard
# TestActionsGateRunsEveryGateStep guards it). Same table rules as tests/lib/gate-steps.sh: one
# step per line "<name> <cores> <task and its vars>", blank lines and # lines ignored, a name
# listed twice keeps its first line. A malformed line is an error (exit 2), never a dropped step.
set -euo pipefail

file="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/Taskfile.lore.yml}"
# The literal block under "  GATE_STEPS: |": every following line indented by four or more
# spaces (or blank) until the first line indented less.
block=$(awk '
  /^  GATE_STEPS: \|[[:space:]]*$/ { on = 1; next }
  on && /^[[:space:]]*$/ { next }
  on && /^    / { print; next }
  on { exit }' "$file")
if [ -z "$block" ]; then
  echo "gate-matrix: no GATE_STEPS block in $file" >&2
  exit 2
fi
printf '%s\n' "$block" | awk '
  function esc(v) { gsub(/\\/, "\\\\", v); gsub(/"/, "\\\"", v); return v }
  { sub(/^[[:space:]]+/, "") }
  $1 == "" || $1 ~ /^#/ { next }
  $1 in seen { next }
  NF < 3 || $2 !~ /^[0-9]+$/ { printf "gate-matrix: bad line in GATE_STEPS: %s\n", $0 > "/dev/stderr"; bad = 1; exit 2 }
  {
    seen[$1] = 1
    name = $1; $1 = ""; $2 = ""; task = $0; sub(/^[[:space:]]+/, "", task)
    out = out (n++ ? "," : "") "{\"step\":\"" esc(name) "\",\"task\":\"" esc(task) "\"}"
  }
  END { if (bad) exit 2; if (n == 0) { print "gate-matrix: GATE_STEPS lists no step" > "/dev/stderr"; exit 2 } print "{\"include\":[" out "]}" }'
