#!/usr/bin/env bash
# gocache-trim.sh: empty Go's build cache when it has grown past a limit. The first step of
# `task check` (Taskfile.lore.yml check:gocache); run it alone with `task lore:check:gocache`.
#
#   LORE_GOCACHE_MAX_GB   the limit in whole GB (default 50); 0 empties the cache every run
#   LORE_GOCACHE_TRIM     "off" skips the step, "dry" only says what it would do
#
# Why: Go keeps every compiled package for 5 days, keyed on its inputs. With about 30 worktrees
# each building and testing every commit, the shared cache (one per user, `go env GOCACHE`)
# reached 1.2 TB and filled the disk. Test builds no longer stamp the version or the worktree
# path (GOFLAGS=-trimpath -buildvcs=false in Taskfile.lore.yml), so worktrees share entries, but
# nothing bounds the cache: this step does. `go clean -cache` is all or nothing (Go has no
# "trim to size"), so the next build after a trim compiles from scratch once.
#
# It touches nothing but the Go cache folder, never a tracked file. The gate holds testlock, so
# no other gate builds while it runs; a quick-tier `go test` of another session at the same
# moment may fail once with a missing cache file and passes on its next run.

set -euo pipefail

limit_gb="${LORE_GOCACHE_MAX_GB:-50}"
mode="${LORE_GOCACHE_TRIM:-on}"

case "$limit_gb" in
  '' | *[!0-9]*)
    echo "✘ LORE_GOCACHE_MAX_GB must be a whole number of GB, got: $limit_gb" >&2
    exit 2
    ;;
esac
case "$mode" in
  on | dry) ;;
  off)
    echo "✓ go build cache: trim off (LORE_GOCACHE_TRIM=off)"
    exit 0
    ;;
  *)
    echo "✘ LORE_GOCACHE_TRIM must be on, dry or off, got: $mode" >&2
    exit 2
    ;;
esac

cache="$(go env GOCACHE)"
if [ -z "$cache" ] || [ "$cache" = "off" ] || [ ! -d "$cache" ]; then
  echo "✓ go build cache: none (GOCACHE=${cache:-unset})"
  exit 0
fi

# du exits 1 when a file vanishes under it (another build writing the cache): the total it
# printed is still the size, so only its output counts.
size_kb="$( (du -sk "$cache" 2>/dev/null || true) | awk 'NR==1{print $1}')"
size_kb="${size_kb:-0}"
limit_kb=$((limit_gb * 1024 * 1024))
size_gb_text="$(awk -v k="$size_kb" 'BEGIN{printf "%.1f", k/1024/1024}')"

if [ "$size_kb" -le "$limit_kb" ]; then
  echo "✓ go build cache $size_gb_text GB, limit $limit_gb GB: kept ($cache)"
  exit 0
fi
if [ "$mode" = dry ]; then
  echo "✓ go build cache $size_gb_text GB, over the $limit_gb GB limit: would empty it with go clean -cache (dry run, $cache)"
  exit 0
fi
go clean -cache
echo "✓ go build cache was $size_gb_text GB, over the $limit_gb GB limit: emptied with go clean -cache ($cache)"
