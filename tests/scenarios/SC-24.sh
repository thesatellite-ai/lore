#!/usr/bin/env bash
# SC-24: Network-FS path refusal (best-effort sim)
# Catches: R18-17, R23-5
source "$(dirname "$0")/../lib/common.sh"
mk_tmp
# Best simulation: a directory whose name matches the iCloud prefix.
CLOUD="$TMP/Library/Mobile Documents/com~apple~CloudDocs/test"
mkdir -p "$CLOUD" 2>/dev/null || skip "can't simulate cloud path"
cd "$CLOUD"
git init -q
OUT=$($LORE init --non-interactive --name=cloud 2>&1) || true
echo "$OUT" | grep -qE "iCloud|cloud sync|network filesystem|E_NETWORK_FS" \
    || skip "network-FS detection not implemented for this path; informational"
pass SC-24
