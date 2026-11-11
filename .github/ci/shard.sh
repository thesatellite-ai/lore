#!/usr/bin/env bash
# shard.sh N I — the packages of shard I (0-based) of N for the quick tier,
# one per line. Packages are dealt longest first to the shard with the least
# time so far (by .github/ci/test-weights.tsv; a package not listed counts
# 5 s), so every shard ends at about the same time. Run from the repo root.
set -euo pipefail
n=$1 i=$2
weights=.github/ci/test-weights.tsv
go list ./... | sed 's#^github.com/thesatellite-ai/lore#.#; s#^\.$#.#' |
	awk -F'\t' -v n="$n" -v i="$i" '
		FNR == NR { if ($0 !~ /^#/) w[$2] = $1; next }
		{ p = ($0 == "") ? "." : $0; t = (p in w) ? w[p] : 5; print t "\t" p }
	' "$weights" - |
	sort -t$'\t' -k1,1rn -k2,2 |
	awk -F'\t' -v n="$n" -v i="$i" '
		{ best = 0; for (s = 1; s < n; s++) if (load[s] < load[best]) best = s
		  load[best] += $1; if (best == i) print $2 }
	'
