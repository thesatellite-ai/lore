#!/usr/bin/env bash
# CH-7: Disk-full mid-write — the write fails cleanly, nothing is corrupted
# Catches: R23-1
#
# Two ways to run out of room, the same checks after each:
#   macOS: a tiny HFS+ disk image is made and mounted without root
#          (hdiutil), filled to the last block, then lore writes into it
#          (the OS answers ENOSPC);
#   Linux: a sized tmpfs needs root, so lore writes under a file-size limit
#          (ulimit -f, no root): every file lore must grow is past the limit,
#          so the write fails the way a full disk fails it (EFBIG).
# Either way: the write is refused as E_DISK_FULL with a hint, no row file
# or temp file is left half-written, every earlier row is still read back,
# the cache passes PRAGMA integrity_check, and once there is room again the
# next write works.
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
need python3
mk_tmp

# A body well past the limit below but under the kernel's one-argument cap.
BIG=$(python3 -c 'print("x"*100000)')
# FSIZE_BLOCKS is the Linux limit in 1 KiB blocks (bash's ulimit -f unit).
FSIZE_BLOCKS=64

if command -v hdiutil >/dev/null 2>&1; then
    IMG="$TMP/full.dmg"; MNT="$TMP/mnt"
    hdiutil create -quiet -size 24m -fs HFS+ -volname lorefull "$IMG" || fail "hdiutil create failed"
    mkdir -p "$MNT"
    hdiutil attach -quiet -nobrowse -mountpoint "$MNT" "$IMG" || fail "hdiutil attach failed"
    detach() { hdiutil detach -quiet -force "$MNT" >/dev/null 2>&1 || true; }
    trap 'detach; cleanup' EXIT
    REPO="$MNT"
else
    REPO="$TMP/repo"
    mkdir -p "$REPO"
fi
cd "$REPO" || fail "cd $REPO"
git init -q
$LORE init --non-interactive --name=full >/dev/null 2>&1 || fail "init"
for i in 1 2 3 4 5; do $LORE memory add --body "before-full-$i" >/dev/null 2>&1 || fail "seed $i"; done
before=$(find .lore/data -type f | sort)

if command -v hdiutil >/dev/null 2>&1; then
    # Fill every remaining block.
    dd if=/dev/zero of="$MNT/filler" bs=1m >/dev/null 2>&1 || true
    dd if=/dev/zero of="$MNT/filler2" bs=4k >/dev/null 2>&1 || true
    OUT=$($LORE memory add --body "$BIG" 2>&1) && fail "a write on a full disk reported success"
    rm -f "$MNT/filler" "$MNT/filler2"
else
    # 1. The cache commit runs out of room: the seeds above left the cache
    #    behind the row files, so the next command's catch-up commit is the
    #    first write past the limit (SQLite reports it as a write I/O error).
    OUT=$( (ulimit -f "$FSIZE_BLOCKS" && exec $LORE memory add --body "$BIG") 2>&1) && fail "a write past the file-size limit reported success"
    echo "$OUT" | grep -q "E_DISK_FULL" || fail "the cache commit past the limit is not E_DISK_FULL: $OUT"
    [ "$(find .lore/data -type f | sort)" = "$before" ] || fail "the refused write left files in .lore/data"
    # 2. The row file runs out of room: a read brings the cache up to date
    #    first, so the refused write is the row file itself.
    $LORE memory list >/dev/null 2>&1 || fail "list"
    OUT=$( (ulimit -f "$FSIZE_BLOCKS" && exec $LORE memory add --body "$BIG") 2>&1) && fail "a write past the file-size limit reported success"
fi
echo "$OUT" | grep -q "E_DISK_FULL" || fail "disk-full error not explained as E_DISK_FULL: $OUT"
echo "$OUT" | grep -q "hint:" || fail "disk-full error names no way out: $OUT"

# Nothing half-written: the row files are exactly the ones from before.
[ "$(find .lore/data -type f | sort)" = "$before" ] || fail "the refused write left files in .lore/data"
sqlite3 "$(repo_cache)" "PRAGMA integrity_check" | grep -qx ok || fail "cache corrupted by the disk-full write"
[ "$(lore_count memories)" -eq 5 ] || fail "lost rows written before the disk filled: $(lore_count memories)/5"
$LORE memory add --body "after-free" >/dev/null 2>&1 || fail "lore did not recover after space was freed"
[ "$(lore_count memories)" -eq 6 ] || fail "the write after freeing space is missing"
pass CH-7
