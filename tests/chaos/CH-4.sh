#!/usr/bin/env bash
# CH-4: Path traversal in toml (companion to SC-23 with deeper path)
# Catches: R27-21
source "$(dirname "$0")/../lib/common.sh"
mk_tmp
mkdir -p .lore
cat > .lore/lore.toml <<TML
db_path = "../../../etc/passwd"
project_id = "prj_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
TML
$LORE memory add "x" 2>&1 | grep -qE "E_BAD_PATH|traversal|outside" || \
    fail "deep path traversal not refused"
pass CH-4
