#!/usr/bin/env bash
# SC-28: Reproducible build sha256 stable
# Catches: R16-25
source "$(dirname "$0")/../lib/common.sh"
need go
WORKSPACE_ROOT="${WORKSPACE_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}"
[ -d "$WORKSPACE_ROOT/cmd/lore" ] || fail "workspace root not found: $WORKSPACE_ROOT"

mk_tmp # (HOME moves into $TMP; go builds keep the caller's HOME for its module and build cache)
(cd "$WORKSPACE_ROOT" && HOME="$LORE_TOOL_HOME" go build -trimpath -ldflags='-buildid= -s -w' -o "$TMP/a1" ./cmd/lore) || fail "build 1"
(cd "$WORKSPACE_ROOT" && HOME="$LORE_TOOL_HOME" go build -trimpath -ldflags='-buildid= -s -w' -o "$TMP/a2" ./cmd/lore) || fail "build 2"
SHA1=$(shasum -a 256 "$TMP/a1" | awk '{print $1}')
SHA2=$(shasum -a 256 "$TMP/a2" | awk '{print $1}')
[ "$SHA1" = "$SHA2" ] || fail "non-reproducible: $SHA1 vs $SHA2"
pass SC-28
