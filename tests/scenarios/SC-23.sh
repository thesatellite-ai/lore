#!/usr/bin/env bash
# SC-23: Path traversal refused in committed config and in paths
# Catches: R27-21
#
# 0.1.18: v0.1.17's .lore/lore.toml (db_path) is gone; the committed file a
# teammate can hand you with a location in it is .lore/stores.yaml. A store
# url that is a bare relative path climbing out of the repo is refused
# (E_STORE_SCHEME, exit 20) on every command, and a --scope dir:<path> that
# climbs out is refused as outside the repo (E_INVALID_INPUT, exit 20).
# Nothing is created outside the checkout.
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; isolate_home; init_project trav
mkdir -p "$TMP/outside"
OUTSIDE="../outside"
REL="../../../../../../../../$TMP/outside"

# 1. stores.yaml naming a store at a path that climbs out of the repo
for url in "$OUTSIDE" "$REL" "../../etc"; do
    printf 'stores:\n    - name: evil\n      url: %s\n' "$url" > .lore/stores.yaml
    for args in "memory add --body x --to evil" "memory list"; do
        # split the words into an array (no unquoted expansion)
        read -r -a argv <<< "$args"
        OUT=$($LORE "${argv[@]}" 2>&1); RC=$?
        [ "$RC" -eq 20 ] || fail "url $url, lore $args: exit $RC, want 20 ($OUT)"
        echo "$OUT" | grep -q "E_STORE_SCHEME" || fail "path traversal in stores.yaml not refused (url $url, lore $args): $OUT"
    done
done
rm .lore/stores.yaml
[ -z "$(ls -A "$TMP/outside")" ] || fail "lore wrote outside the repo: $(ls -A "$TMP/outside")"

# 2. a dir scope that climbs out of the repo
for p in "../outside" "a/../../outside" "../../etc"; do
    OUT=$($LORE memory add --body x --scope "dir:$p" 2>&1); RC=$?
    [ "$RC" -eq 20 ] || fail "dir:$p: exit $RC, want 20 ($OUT)"
    echo "$OUT" | grep -q "outside the repo" || fail "dir scope $p not refused: $OUT"
done

# 3. control: a dir scope inside the repo is accepted, and lore works again
mkdir -p src/api
$LORE memory add --body "inside" --scope "dir:src/api" >/dev/null || fail "dir scope inside the repo refused"
$LORE memory list >/dev/null || fail "memory list after removing the bad stores.yaml"
[ -z "$(ls -A "$TMP/outside")" ] || fail "lore wrote outside the repo"
pass SC-23
