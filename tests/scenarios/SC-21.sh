#!/usr/bin/env bash
# SC-21: Refuse-root unless LORE_ALLOW_ROOT=1 (legacy MINI_ALLOW_ROOT=1 still honoured)
# Catches: R16-14, R29-36
#
# Needs root: runs directly when already root, through passwordless sudo
# when available, and skips otherwise (the refusal itself is also unit-tested
# with an injected uid: saas/cmd/cli/guard_cli_test.go).
source "$(dirname "$0")/../lib/common.sh"
if [ "$(id -u)" -eq 0 ]; then
    AS_ROOT=(env)
elif sudo -n true 2>/dev/null; then
    AS_ROOT=(sudo -n env)
else
    skip "needs root or passwordless sudo (covered by TestRootRefusal)"
fi
mk_tmp
git init -q
"${AS_ROOT[@]}" $LORE init --non-interactive --name=root 2>&1 | grep -qE "refuses to run as root|E_ROOT_REFUSED" || \
    fail "did not refuse root without override"
"${AS_ROOT[@]}" LORE_ALLOW_ROOT=1 $LORE init --non-interactive --name=rootok >/dev/null || fail "override did not unblock"
# The override run left root-owned files that the unprivileged EXIT trap
# cannot delete, so remove the whole temp dir as root here.
cd /
"${AS_ROOT[@]}" rm -rf "$TMP" || fail "could not remove root-owned temp dir $TMP"
pass SC-21
