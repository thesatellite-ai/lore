#!/usr/bin/env bash
# ci-summary.sh: the verdict of the Actions gate (.github/workflows/gate.yml, job summary).
#
#   STEPS_MATRIX='{"include":[...]}' WARM_RESULT=success tests/lib/ci-summary.sh <results folder> >> "$GITHUB_STEP_SUMMARY"
#
# Prints a markdown table, one row per GATE_STEPS step (from the matrix) plus docs-index and the
# recall report: PASS, FAIL or NO RESULT (the job died, timed out or never started) and its wall time.
# Exits 1 when the warm job failed or any step but recall did not PASS, so the summary job's
# conclusion is the gate's verdict. The recall report never fails it: its summary line is printed.
set -uo pipefail

dir="${1:?usage: $0 <results folder>}"
steps=$(printf '%s' "${STEPS_MATRIX:-}" | python3 -c 'import json,sys
try:
    m = json.load(sys.stdin)
except ValueError:
    sys.exit(0)
for e in m.get("include", []):
    print(e["step"])')
if [ -z "$steps" ]; then
  echo "### Gate: FAIL"
  echo
  echo "The steps job produced no matrix (GATE_STEPS could not be read)."
  exit 1
fi

fail=0
rows=""
total=0
for s in $steps docs-index recall; do
  f="$dir/result-$s.tsv"
  if [ -f "$f" ]; then
    IFS=$'\t' read -r _ verdict wall note <"$f"
  else
    verdict="NO RESULT"
    wall="-"
    note=""
  fi
  total=$((total + 1))
  kind="gate"
  if [ "$s" = recall ]; then
    kind="report"
  elif [ "$verdict" != PASS ]; then
    fail=$((fail + 1))
  fi
  rows="$rows| $s | $kind | $verdict | $wall | ${note:-} |"$'\n'
done

if [ "${WARM_RESULT:-success}" != success ]; then
  fail=$((fail + 1))
fi
if [ "$fail" -eq 0 ]; then
  echo "### Gate: PASS"
else
  echo "### Gate: FAIL ($fail failed)"
fi
echo
echo "Commit \`${GATE_REF:-?}\`, $total steps, warm job: ${WARM_RESULT:-?}."
echo
echo "| step | kind | result | wall s | note |"
echo "|---|---|---|---|---|"
printf '%s' "$rows"
[ "$fail" -eq 0 ]
