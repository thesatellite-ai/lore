#!/usr/bin/env bash
# SC-21: Refuse-root unless MINI_ALLOW_ROOT=1
# Catches: R16-14, R29-36
source "$(dirname "$0")/../lib/common.sh"
[ "$(id -u)" -eq 0 ] || skip "not running as root"
mk_tmp
git init -q
$LORE init --non-interactive --name=root 2>&1 | grep -qE "refuses to run as root|MINI_ALLOW_ROOT" || \
    fail "did not refuse root without override"
MINI_ALLOW_ROOT=1 $LORE init --non-interactive --name=rootok >/dev/null || fail "override did not unblock"
pass SC-21
