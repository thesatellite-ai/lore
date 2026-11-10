#!/usr/bin/env bash
# CH-12: Ambiguous .lore/ (a project of its own and a shared-database pointer)
# Catches: R18-1
#
# 0.1.18: a repo's project is its row files in .lore/data; .lore/lore.toml
# and .lore/lore.yaml are retired shared-database (Mode B) pointers. A
# stray pointer beside a real project must not be silently followed or
# silently ignored: every command refuses (E_UNSUPPORTED, exit 23) naming
# the pointer file and lore migrate run, and works again once it is gone.
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; isolate_home; init_project ch12
$LORE memory add --body "own project row" >/dev/null || fail "add"

for f in lore.toml lore.yaml; do
    if [ "$f" = lore.toml ]; then
        printf 'db_path = "%s/other.db"\nproject_id = "prj_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"\n' "$TMP" > ".lore/$f"
    else
        printf 'db_path: %s/other.db\nproject_id: prj_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n' "$TMP" > ".lore/$f"
    fi
    for args in "memory search x" "memory add --body y"; do
        read -r -a argv <<< "$args"
        OUT=$($LORE "${argv[@]}" 2>&1); RC=$?
        [ "$RC" -eq 23 ] || fail "$f, lore $args: exit $RC, want 23 ($OUT)"
        echo "$OUT" | grep -q "retired shared-database mode (.lore/$f)" || fail "ambiguous .lore/ ($f) not refused: $OUT"
        echo "$OUT" | grep -q "lore migrate run" || fail "refusal does not name lore migrate run: $OUT"
    done
    [ ! -e "$TMP/other.db" ] || fail "lore opened the pointer's database"
    rm ".lore/$f"
done

$LORE memory search "own" 2>&1 | grep -q "own project row" || fail "project unreadable after removing the pointer"
pass CH-12
