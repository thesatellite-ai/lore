#!/usr/bin/env bash
# CH-4: Path traversal in toml (companion to SC-23 with deeper path)
# Catches: R27-21
#
# 0.1.18: .lore/lore.toml is a retired shared-database (Mode B) pointer.
# Every command refuses the repo (E_UNSUPPORTED, exit 23, naming lore
# migrate run) without opening the db_path, and lore migrate run (and its
# dry run) refuse a db_path with a `..` part (E_BAD_PATH, exit 20) leaving
# the pointer as it was. A deep dir scope that climbs out is refused too.
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; isolate_home
mkdir "$TMP/ptr" && cd "$TMP/ptr"
git init -q
mkdir -p .lore
cat > .lore/lore.toml <<TML
db_path = "../../../etc/passwd"
project_id = "prj_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
TML
POINTER_SUM=$(cksum < .lore/lore.toml)

OUT=$($LORE memory add --body "x" 2>&1); RC=$?
[ "$RC" -eq 23 ] || fail "memory add with a Mode B pointer: exit $RC, want 23 ($OUT)"
echo "$OUT" | grep -q "retired shared-database mode (.lore/lore.toml)" || fail "pointer not refused: $OUT"

for args in "migrate run --dry-run" "migrate run"; do
    read -r -a argv <<< "$args"
    OUT=$($LORE "${argv[@]}" 2>&1); RC=$?
    [ "$RC" -eq 20 ] || fail "lore $args: exit $RC, want 20 ($OUT)"
    echo "$OUT" | grep -q 'E_BAD_PATH' || fail "deep path traversal not refused by lore $args: $OUT"
done
[ "$(cksum < .lore/lore.toml)" = "$POINTER_SUM" ] || fail "a refused migrate changed the pointer"
[ ! -e .lore/stores.yaml ] || fail "a refused migrate wrote .lore/stores.yaml"

# deep dir scope in a real project
mkdir "$TMP/proj" && cd "$TMP/proj" && init_project ch4
OUT=$($LORE memory add --body x --scope "dir:a/b/../../../../../../etc" 2>&1); RC=$?
[ "$RC" -eq 20 ] || fail "deep dir scope: exit $RC, want 20 ($OUT)"
echo "$OUT" | grep -q "outside the repo" || fail "deep dir scope traversal not refused: $OUT"
pass CH-4
