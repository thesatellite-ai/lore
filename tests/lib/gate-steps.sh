#!/usr/bin/env bash
# gate-steps.sh: run gate steps listed in a table (Taskfile.lore.yml GATE_STEPS) through
# tests/lib/gate-parallel.sh, all of them (`task check`) or the ones named (`task check:fast`).
#
#   GATE_STEPS="<table>" GATE_SELF="task --taskfile …" tests/lib/gate-steps.sh <stage> [name...]
#
# The table has one step per line, "<name> <cores> <task and its vars>" (blank lines and lines
# starting with # are ignored); each step runs as "$GATE_SELF <task and its vars>" and counts
# <cores> against GATE_SLOTS. With names given, only those steps run, still in table order (the
# table is longest first); a name the table does not have is a usage error (exit 2), so a
# mapping that names a step which was renamed or removed fails loudly instead of testing less.
# One table for both gates: check:fast runs a subset of exactly the steps the full gate runs
# (plus its own race-changed step, and with some steps given a larger share; see below).
set -uo pipefail

if [ "$#" -lt 1 ] || [ -z "${GATE_STEPS:-}" ] || [ -z "${GATE_SELF:-}" ]; then
  echo "usage: GATE_STEPS=<table> GATE_SELF=<task command> $0 <stage> [name...]" >&2
  exit 2
fi
stage="$1"
shift
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# A name listed twice keeps its first line (place and definition), so a caller can put lines in
# front of the table that start a step earlier or give it another share (check:fast gives
# userscen more cores, since it runs beside fewer steps than in the full gate).
table=$(printf '%s\n' "$GATE_STEPS" | awk '
  $1 == "" || $1 ~ /^#/ { next }
  !($1 in seen) { seen[$1] = 1; print }')

known=" "
specs=()
while read -r name cores rest; do
  case "$name" in '' | '#'*) continue ;; esac
  if [ -z "$cores" ] || [ -z "$rest" ]; then
    echo "gate-steps: bad line in GATE_STEPS: $name $cores $rest" >&2
    exit 2
  fi
  known="$known$name "
  if [ "$#" -gt 0 ]; then
    case " $* " in *" $name "*) ;; *) continue ;; esac
  fi
  specs+=("$name:$cores=$GATE_SELF $rest")
done <<EOF
$table
EOF

for want in "$@"; do
  case "$known" in *" $want "*) ;; *)
    echo "gate-steps: no step named $want in GATE_STEPS" >&2
    exit 2
    ;;
  esac
done
if [ "${#specs[@]}" -eq 0 ]; then
  echo "gate-steps: no step to run" >&2
  exit 2
fi
exec "$here/gate-parallel.sh" "$stage" "${specs[@]}"
