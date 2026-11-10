#!/usr/bin/env bash
# SC-15: doctor JSON schema stable + exit codes
# Catches: R22-38, R25-22, R29-49
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; init_project doc

OUT=$($LORE doctor --json) || fail "doctor json: $OUT"
echo "$OUT" | json_true 'd["schema_version"] == 1' >/dev/null || fail "schema_version != 1"
echo "$OUT" | json_true 'd["db_ok"] is True' >/dev/null || fail "db_ok not true on healthy DB"
echo "$OUT" | json_true 'd.get("identity")' >/dev/null || fail "identity block missing"
$LORE doctor >/dev/null
[ $? -eq 0 ] || fail "doctor exit non-zero on healthy DB"
pass SC-15
