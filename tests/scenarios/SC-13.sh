#!/usr/bin/env bash
# SC-13: Symlink-aware render (writes through symlink, preserves link)
# Catches: R27-19
#
# 0.1.18: render writes only the tool files that are wired (context.targets
# or --target); an existing CLAUDE.md that is not wired is named, never
# written. Once claude_code is the target, the pointer goes through the
# link into the link's target and the link itself stays.
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; isolate_home; init_project symlinkr
$LORE rule add --severity=must --body "linkpayload" >/dev/null || fail "rule add"
mkdir -p shared
touch shared/CLAUDE.md
ln -sf shared/CLAUDE.md CLAUDE.md

# not wired: CLAUDE.md is named, left as it is
OUT=$($LORE render 2>&1) || fail "render: $OUT"
echo "$OUT" | grep -q "CLAUDE.md found but not wired" || fail "unwired CLAUDE.md not named: $OUT"
[ -L CLAUDE.md ] || fail "symlink was replaced by file (unwired render)"
[ ! -s shared/CLAUDE.md ] || fail "unwired render wrote into CLAUDE.md"

# wired: written through the link, twice (the second render changes nothing)
for i in 1 2; do
    $LORE render --target claude_code >/dev/null || fail "render --target claude_code ($i)"
    [ -L CLAUDE.md ] || fail "symlink was replaced by file ($i)"
    [ "$(readlink CLAUDE.md)" = "shared/CLAUDE.md" ] || fail "symlink retargeted ($i)"
    [ -s shared/CLAUDE.md ] || fail "target file is empty ($i)"
    grep -q "@.lore/LORE.md" shared/CLAUDE.md || fail "target missing @import pointer ($i)"
    [ "$(grep -c "lore:pointer:start" shared/CLAUDE.md)" -eq 1 ] || fail "pointer block duplicated ($i)"
done
grep -q "linkpayload" .lore/LORE.md || fail "LORE.md missing must rule"
pass SC-13
