#!/usr/bin/env bash
# gofmt.sh: run the gofmt of the Go version go.mod names (its `go` line), whatever Go is installed.
#
#   tests/lib/gofmt.sh -l .      list the files that version would change (lint:fmt)
#   tests/lib/gofmt.sh -w .      rewrite them (fmt)
#
# Why: gofmt's alignment rules change between Go releases, and two releases can each reject the
# other's output. The gate on GitHub Actions (.github/workflows/gate.yml) installs go.mod's version;
# a machine with a newer Go passed lint:fmt on a file the Actions gofmt flagged (go1.27 vs go1.26,
# 2026-11-23). Running go.mod's version everywhere makes the local step and the Actions step agree.
# GOTOOLCHAIN=go<version> makes the go command fetch that toolchain once (Go's own download, cached
# in the module cache) when it is not the installed one; `go env GOROOT` then names its folder.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
want="$(awk '$1 == "go" { print $2; exit }' "$root/go.mod")"
if [ -z "$want" ]; then
  echo "gofmt.sh: no go line in $root/go.mod" >&2
  exit 2
fi
goroot="$(cd "$root" && GOTOOLCHAIN="go$want" GOFLAGS= go env GOROOT)"
exec "$goroot/bin/gofmt" "$@"
