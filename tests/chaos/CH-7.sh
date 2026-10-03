#!/usr/bin/env bash
# CH-7: Disk-full mid-write — the write fails cleanly, nothing is corrupted
# Catches: R23-1
#
# macOS: a tiny HFS+ disk image is created and mounted without root
# (hdiutil), filled to the last block, then lore writes into it.
# Linux: a size-limited tmpfs needs root, so the scenario skips there.
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
command -v hdiutil >/dev/null 2>&1 || skip "disk-full sim needs hdiutil (macOS); Linux needs root for a sized tmpfs"
mk_tmp

IMG="$TMP/full.dmg"; MNT="$TMP/mnt"
hdiutil create -quiet -size 24m -fs HFS+ -volname lorefull "$IMG" || skip "hdiutil create failed"
mkdir -p "$MNT"
hdiutil attach -quiet -nobrowse -mountpoint "$MNT" "$IMG" || skip "hdiutil attach failed"
detach() { hdiutil detach -quiet -force "$MNT" >/dev/null 2>&1 || true; }
trap 'detach; cleanup' EXIT

cd "$MNT"
git init -q
$LORE init --non-interactive --name=full >/dev/null 2>&1 || fail "init on the image"
for i in 1 2 3 4 5; do $LORE memory add --body "before-full-$i" >/dev/null 2>&1 || fail "seed $i"; done

# Fill every remaining block.
dd if=/dev/zero of="$MNT/filler" bs=1m >/dev/null 2>&1 || true
dd if=/dev/zero of="$MNT/filler2" bs=4k >/dev/null 2>&1 || true

BIG=$(python3 -c 'print("x"*200000)')
OUT=$($LORE memory add --body "$BIG" 2>&1) && fail "a write on a full disk reported success"
echo "$OUT" | grep -qiE "full|no space|E_DISK_FULL" || fail "disk-full error not explained: $OUT"

# Free space; the DB must be intact and keep every earlier row.
rm -f "$MNT/filler" "$MNT/filler2"
sqlite3 .lore/lore.db "PRAGMA integrity_check" | grep -qx ok || fail "DB corrupted by the disk-full write"
COUNT=$(sqlite3 .lore/lore.db "SELECT COUNT(*) FROM memories WHERE body LIKE 'before-full-%'")
[ "$COUNT" -eq 5 ] || fail "lost rows written before the disk filled: $COUNT/5"
$LORE memory add --body "after-free" >/dev/null 2>&1 || fail "lore did not recover after space was freed"
pass CH-7
