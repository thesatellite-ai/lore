#!/bin/sh
# Prints the git tree id of the tracked files exactly as they are in the work
# tree (staged or not; untracked files are ignored), without touching the
# real index. `task check` records it before and after the gate; the
# pre-push hook compares the after-value with the commits being pushed.
set -e
index=$(mktemp)
trap 'rm -f "$index"' EXIT
GIT_INDEX_FILE="$index" git read-tree HEAD
GIT_INDEX_FILE="$index" git add -u
GIT_INDEX_FILE="$index" git write-tree
