#!/usr/bin/env bash
# CH-7: Disk-full mid-write (requires tmpfs sandbox)
# Catches: R23-1
source "$(dirname "$0")/../lib/common.sh"
skip "disk-full sim requires CI tmpfs setup (not portable)"
