#!/usr/bin/env bash
# SC-23: Path traversal refused in toml
# Catches: R27-21
source "$(dirname "$0")/../lib/common.sh"
mk_tmp
mkdir -p .lore
cat > .lore/lore.toml <<TML
db_path = "../../etc/passwd"
project_id = "prj_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
TML
$LORE memory search "x" 2>&1 | grep -qE "E_BAD_PATH|traversal|outside" || \
    fail "path traversal in toml not refused"
pass SC-23
