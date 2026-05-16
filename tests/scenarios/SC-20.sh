#!/usr/bin/env bash
# SC-20: Secret-pattern scrub refuses pasted API key
# Catches: R16-12, R23-17
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project sec
for key in \
    "Use this key: AKIAIOSFODNN7EXAMPLE for staging" \
    "OpenAI key sk-1234567890abcdef1234567890ab" \
    "ghp_1234567890abcdefghijklmnopqrstuvwxyz12"; do
    $LORE memory add "$key" 2>&1 | grep -qE "E_SECRET_DETECTED|secret" || fail "secret not refused: $key"
done
pass SC-20
