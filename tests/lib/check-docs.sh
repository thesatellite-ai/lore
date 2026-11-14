#!/usr/bin/env bash
# check-docs.sh: the docsync gate step (task lore:check:docs). Docs cite
# code through docsync; `ds scan` refreshes the bindings and `ds check`
# fails when a cited block changed since the sentence was written.
#
# `ds scan` writes into .ds/: it rewrites ledger.tsv and refs.tsv (their
# header records the commit and time of the scan) and adds a block file
# under .ds/blocks/ for every block whose body changed. A gate must leave
# the tree as it found it, so the whole .ds/ folder is copied aside first
# and put back exactly afterwards: tracked files restored, block files the
# scan created removed (run:docs:ds-stray-blocks: they were left untracked
# and removed by hand after every run). Refresh the bindings on purpose
# with `ds scan` and commit them when docs or citations change.
#
# Run from the repository root (the task does). Exit status is ds's.
set -u

# `go install` puts ds in $(go env GOPATH)/bin, which a gate's PATH (a
# non-login shell, the after-land runner) may not hold: look there too.
if ! command -v ds >/dev/null 2>&1; then
  PATH="$PATH:$(go env GOPATH)/bin"
fi
if ! command -v ds >/dev/null 2>&1; then
  echo "✘ ds (docsync) is not installed (not on PATH, nor in \$(go env GOPATH)/bin), so doc citations cannot be checked"
  exit 1
fi
if [ ! -d .ds ]; then
  echo "✘ no .ds folder here: run from the repository root"
  exit 1
fi

keep=$(mktemp -d)
trap 'rm -rf "$keep"' EXIT
cp -Rp .ds "$keep/ds"
status=0
ds scan >/dev/null && ds check || status=$?
rm -rf .ds
mv "$keep/ds" .ds
exit $status
