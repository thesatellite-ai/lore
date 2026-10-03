#!/usr/bin/env bash
# SC-13: Symlink-aware render (writes through symlink, preserves link)
# Catches: R27-19
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project symlinkr
$LORE rule add --severity=must --body "linkpayload" >/dev/null
mkdir -p shared
touch shared/CLAUDE.md
ln -sf shared/CLAUDE.md CLAUDE.md
$LORE render >/dev/null || fail "render"
[ -L CLAUDE.md ] || fail "symlink was replaced by file"
[ -s shared/CLAUDE.md ] || fail "target file is empty"
grep -q "@.lore/LORE.md" shared/CLAUDE.md || fail "target missing @import pointer"
grep -q "linkpayload" .lore/LORE.md || fail "LORE.md missing must rule"
pass SC-13
