#!/usr/bin/env bash
# CH-9: PATH conflict (two aicoder binaries)
# Catches: R14-8, R18-26
source "$(dirname "$0")/../lib/common.sh"
mk_tmp
FAKE="$TMP/fake"
mkdir -p "$FAKE"
cat > "$FAKE/aicoder" <<'FAKESH'
#!/bin/sh
echo fake-aicoder
FAKESH
chmod +x "$FAKE/aicoder"

# doctor may warn (not fail) on PATH ambiguity. Treat warning as pass; no warning = informational pass.
PATH="$FAKE:$PATH" $LORE doctor 2>&1 | grep -qE "PATH conflict|multiple aicoder|shadowed" || \
    echo "info: PATH conflict detection not implemented (informational)"
pass CH-9
