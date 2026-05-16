#!/usr/bin/env bash
# run-all.sh — execute every SC-* and CH-* sequentially. Summary at end.
set -u
LORE="${LORE:-/tmp/aicoder}"
export LORE

PASS=0; FAIL=0; SKIP=0
FAILED=()
SKIPPED=()

run() {
    local f="$1"
    local out
    out=$(bash "$f" 2>&1)
    local rc=$?
    local name; name=$(basename "$f" .sh)
    if [ $rc -ne 0 ]; then
        FAIL=$((FAIL+1))
        FAILED+=("$name: $(echo "$out" | tail -1)")
        printf "  ✘ %-8s %s\n" "$name" "$(echo "$out" | tail -1)"
    elif echo "$out" | grep -q "^SKIP:"; then
        SKIP=$((SKIP+1))
        SKIPPED+=("$name: $(echo "$out" | grep ^SKIP: | head -1)")
        printf "  ⏭  %-8s %s\n" "$name" "$(echo "$out" | grep ^SKIP: | head -1 | sed 's/^SKIP: //')"
    else
        PASS=$((PASS+1))
        printf "  ✓ %-8s\n" "$name"
    fi
}

echo "==== Acceptance scenarios ===="
for f in "$(dirname "$0")"/scenarios/SC-*.sh; do run "$f"; done
echo
echo "==== Chaos scenarios ===="
for f in "$(dirname "$0")"/chaos/CH-*.sh; do run "$f"; done

echo
echo "Total:   $((PASS+FAIL+SKIP))"
echo "Pass:    $PASS"
echo "Fail:    $FAIL"
echo "Skip:    $SKIP"
[ "$FAIL" -eq 0 ] || exit 1
