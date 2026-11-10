#!/usr/bin/env bash
# SC-21: Refuse-root unless LORE_ALLOW_ROOT=1 (legacy MINI_ALLOW_ROOT=1 still honoured)
# Catches: R16-14, R29-36
#
# The refusal needs euid 0. As root, or through passwordless sudo, the real
# binary is run as root. Otherwise (the gate user has neither) the script
# still runs every part that needs no root — lore works as a normal user, and
# LORE_ALLOW_ROOT changes nothing there — and the euid-0 refusal itself is
# proven by TestRootRefusal (cmd/lore/guard_test.go, an injected uid) in the
# unit gate. Never a skip: the part this machine can check always runs.
source "$(dirname "$0")/../lib/common.sh"
mk_tmp
git init -q
$LORE init --non-interactive --name=user >/dev/null || fail "a normal user was refused"
LORE_ALLOW_ROOT=1 $LORE memory add --body "as a user with the override set" >/dev/null || fail "LORE_ALLOW_ROOT broke a normal user's run"
if [ "$(id -u)" -eq 0 ]; then
    AS_ROOT=(env)
elif sudo -n true 2>/dev/null; then
    AS_ROOT=(sudo -n env)
else
    echo "info: no root here; the euid-0 refusal is proven by TestRootRefusal (cmd/lore/guard_test.go)"
    pass SC-21
fi
mkdir root && cd root || fail "mkdir root"
git init -q
"${AS_ROOT[@]}" $LORE init --non-interactive --name=root 2>&1 | grep -qE "refuses to run as root|E_ROOT_REFUSED" || \
    fail "did not refuse root without override"
"${AS_ROOT[@]}" LORE_ALLOW_ROOT=1 $LORE init --non-interactive --name=rootok >/dev/null || fail "override did not unblock"
# The override run left root-owned files that the unprivileged EXIT trap
# cannot delete, so remove the whole temp dir as root here.
cd /
"${AS_ROOT[@]}" rm -rf "$TMP" || fail "could not remove root-owned temp dir $TMP"
pass SC-21
