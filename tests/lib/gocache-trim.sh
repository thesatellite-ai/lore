#!/usr/bin/env bash
# gocache-trim.sh: trim Go's build cache when it has grown past a limit. The first step of
# `task check` (Taskfile.lore.yml check:gocache); run it alone with `task lore:check:gocache`.
#
#   LORE_GOCACHE_MAX_GB    the limit in whole GB (default 50)
#   LORE_GOCACHE_IDLE_MIN  over the limit, entries unused for this many minutes are deleted
#                          (default 120)
#   LORE_GOCACHE_TRIM      "off" skips the step, "dry" only says what it would do
#
# Why: Go keeps every compiled package for 5 days, keyed on its inputs. With about 30 worktrees
# each building and testing every commit, the shared cache (one per user, `go env GOCACHE`)
# reached 1.2 TB and filled the disk. Test builds no longer stamp the version or the worktree
# path (GOFLAGS=-trimpath -buildvcs=false in Taskfile.lore.yml), so worktrees share entries, but
# nothing bounds the cache: this step does.
#
# Builds run while it trims: the gate holds testlock, but check:fast and the quick tier take no
# lock. So it never empties the whole cache (`go clean -cache` deleted files under running
# builds, which then failed, and itself failed on "directory not empty" when a build wrote a new
# entry mid-clean). It deletes only entries idle for LORE_GOCACHE_IDLE_MIN minutes: Go refreshes
# an entry's mtime when a build uses it and the mtime is over an hour old, so an entry idle for
# two hours is in no running build. A file that vanishes or appears meanwhile is not an error.
# It touches nothing but the Go cache folder, never a tracked file.

set -euo pipefail

limit_gb="${LORE_GOCACHE_MAX_GB:-50}"
idle_min="${LORE_GOCACHE_IDLE_MIN:-120}"
mode="${LORE_GOCACHE_TRIM:-on}"

case "$limit_gb" in
  '' | *[!0-9]*)
    echo "✘ LORE_GOCACHE_MAX_GB must be a whole number of GB, got: $limit_gb" >&2
    exit 2
    ;;
esac
case "$idle_min" in
  '' | *[!0-9]*)
    echo "✘ LORE_GOCACHE_IDLE_MIN must be a whole number of minutes, got: $idle_min" >&2
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
measure() { (du -sk "$cache" 2>/dev/null || true) | awk 'NR==1{print $1}'; }
gb() { awk -v k="$1" 'BEGIN{printf "%.1f", k/1024/1024}'; }
size_kb="$(measure)"
size_kb="${size_kb:-0}"
limit_kb=$((limit_gb * 1024 * 1024))
size_gb_text="$(gb "$size_kb")"

if [ "$size_kb" -le "$limit_kb" ]; then
  echo "✓ go build cache $size_gb_text GB, limit $limit_gb GB: kept ($cache)"
  exit 0
fi
if [ "$mode" = dry ]; then
  echo "✓ go build cache $size_gb_text GB, over the $limit_gb GB limit: would delete entries idle over $idle_min min (dry run, $cache)"
  exit 0
fi
# Entries live one level down (<cache>/<2 hex>/<hash>-a|-d); the cache's own files at its top
# (README, trim.txt) stay. find's errors (a file deleted or written by a running build) are
# expected and ignored.
find "$cache" -mindepth 2 -type f -mmin "+$idle_min" -delete 2>/dev/null || true
after_kb="$(measure)"
after_kb="${after_kb:-0}"
if [ "$after_kb" -le "$limit_kb" ]; then
  echo "✓ go build cache was $size_gb_text GB, over the $limit_gb GB limit: deleted entries idle over $idle_min min, now $(gb "$after_kb") GB ($cache)"
else
  echo "⚠ go build cache was $size_gb_text GB, over the $limit_gb GB limit: deleted entries idle over $idle_min min, still $(gb "$after_kb") GB (in use by recent builds; the next gate trims again) ($cache)"
fi
