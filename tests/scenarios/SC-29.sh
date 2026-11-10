#!/usr/bin/env bash
# SC-29: lore version --json schema
# Catches: R25-3, R25-11
source "$(dirname "$0")/../lib/common.sh"
need python3
OUT=$($LORE version --json) || fail "version --json"
echo "$OUT" | json_true 'd.get("binary_version")' >/dev/null || fail "binary_version missing"
echo "$OUT" | json_true 'd.get("schema_version")' >/dev/null || fail "schema_version missing"
# Optional fields — log absence but don't fail (v0.1 may omit MCP).
echo "$OUT" | json_true 'd.get("bundle_format_version")' >/dev/null || echo "info: bundle_format_version absent"
echo "$OUT" | json_true 'd.get("plugin_protocol_version")' >/dev/null || echo "info: plugin_protocol_version absent (v0.2)"
echo "$OUT" | json_true 'd.get("mcp_tool_version")' >/dev/null || echo "info: mcp_tool_version absent (v0.2)"
pass SC-29
