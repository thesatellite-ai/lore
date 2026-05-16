#!/usr/bin/env bash
# CH-5: Symlink loop in learn-from — must not hang
# Catches: R18-7
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project ch5

mkdir -p test/loop
(cd test/loop && ln -s . self)

# Use a 10s timeout — any non-124 exit is acceptable; 124 = hung.
timeout 10 $LORE learn-from docs --paths='test/**/*.md' >/dev/null 2>&1
RC=$?
[ "$RC" -ne 124 ] || fail "learn-from hung on symlink loop"
pass CH-5
