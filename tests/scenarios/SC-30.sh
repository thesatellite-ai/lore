#!/usr/bin/env bash
# SC-30: the error catalog — every code lore can print is listed with its exit
# Catches: R29-50
source "$(dirname "$0")/../lib/common.sh"
mk_tmp

# `lore spec docs` writes the errors block of cli-commands.md from the
# binary's own registry; a page with empty blocks takes it.
mkdir ref
printf '%s\n' '# ref' '<!-- lore:spec:begin commands -->' '<!-- lore:spec:end commands -->' \
    '<!-- lore:spec:begin errors -->' '<!-- lore:spec:end errors -->' > ref/cli-commands.md
$LORE spec docs --out ref >/dev/null || fail "spec docs"
ERRS=$(sed -n '/lore:spec:begin errors/,/lore:spec:end errors/p' ref/cli-commands.md)
LEN=$(printf '%s\n' "$ERRS" | grep -c '^| `E_')
[ "$LEN" -ge 20 ] || fail "expected ≥20 error codes, got $LEN"
for code in E_DB_LOCKED E_SECRET_DETECTED E_NOT_FOUND E_ROOT_REFUSED; do
    printf '%s\n' "$ERRS" | grep -q "^| \`$code\`" || fail "$code missing from the error catalog"
done
pass SC-30
