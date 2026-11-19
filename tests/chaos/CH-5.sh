#!/usr/bin/env bash
# CH-5: Symlink loop in learn-from — must not hang
# Catches: R18-7
source "$(dirname "$0")/../lib/common.sh"
# Resolved before mk_tmp changes the folder.
TESTTIME="$(cd "$(dirname "$0")/../lib" && pwd)/testtime.sh"
mk_tmp; init_project ch5

mkdir -p test/loop
(cd test/loop && ln -s . self)

# A 10 s timeout times the machine factor (tests/lib/testtime.sh): any non-124 exit is
# acceptable; 124 = hung.
limit=$("$TESTTIME" 10s) || fail "tests/lib/testtime.sh refused the limit"
timeout "$limit" $LORE learn-from docs --paths='test/**/*.md' >/dev/null 2>&1
RC=$?
[ "$RC" -ne 124 ] || fail "learn-from hung on symlink loop"
pass CH-5
