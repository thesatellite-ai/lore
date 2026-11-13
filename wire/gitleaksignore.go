package wire

// gitleaksignore.go — a repo's .gitleaksignore in lore's commit and push
// checks (user report: a repo moving from gitleaks to lore had its
// accepted findings listed there, and lore refused them all). Each line is
// a gitleaks fingerprint: "<path>:<rule>:<line>" (gitleaks' staged form)
// or "<commit>:<path>:<rule>:<line>" (a history scan). Only the staged
// form counts here (D17, as gitleaks does): a commit-pinned line accepts a
// finding in that one commit, and a staged file has no commit yet, so
// honouring it by path and line would also pass any new value later put
// on that line. Like the secret_allows entries, only the file as it
// stands in the index counts.

import (
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/logic/scan"
)

// gitleaksIgnoreFile is the file gitleaks reads at the repo top.
const gitleaksIgnoreFile = ".gitleaksignore"

// hookAllows is everything a commit or push check accepts as not a
// secret: the committed secret_allows entries and the repo's
// .gitleaksignore fingerprints.
type hookAllows struct {
	entries []scan.Allow
	ignore  gitleaksIgnore
}

// ignoreKey is one fingerprint without its commit.
type ignoreKey struct {
	path, rule string
	line       int
}

// gitleaksIgnore is the set of a .gitleaksignore's fingerprints.
type gitleaksIgnore map[ignoreKey]bool

// parseGitleaksIgnore reads a .gitleaksignore: blank lines and "#"
// comments are skipped, as is any line that is not a staged-form
// fingerprint (a commit-pinned line belongs to history scans, D17).
func parseGitleaksIgnore(b []byte) gitleaksIgnore {
	out := gitleaksIgnore{}
	for _, raw := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		parts := strings.Split(l, ":")
		n := len(parts)
		if n < 3 {
			continue
		}
		line, err := strconv.Atoi(parts[n-1])
		if err != nil || line < 1 || parts[n-2] == "" {
			continue
		}
		head := parts[:n-2]
		if len(head) > 1 && isCommitID(head[0]) {
			continue // <commit>:<path>:<rule>:<line>: a history-scan entry
		}
		p := strings.Join(head, ":")
		if p == "" {
			continue
		}
		out[ignoreKey{path: p, rule: parts[n-2], line: line}] = true
	}
	return out
}

// isCommitID reports whether s looks like an abbreviated or full commit id.
func isCommitID(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// drop removes the hits of the file at p that a fingerprint names. A
// fingerprint may name lore's rule or a gitleaks rule it covers
// (gitleaks.Covers): the refusal reports one value once, under lore's id,
// while the file was written for gitleaks.
func (g gitleaksIgnore) drop(p string, hits []leakcheck.SecretHit) []leakcheck.SecretHit {
	if len(g) == 0 || len(hits) == 0 {
		return hits
	}
	covers := gitleaks.Covers()
	out := hits[:0:0]
	for _, h := range hits {
		ok := g[ignoreKey{path: p, rule: h.Rule, line: h.Line}]
		for _, alt := range covers[h.Rule] {
			ok = ok || g[ignoreKey{path: p, rule: alt, line: h.Line}]
		}
		if !ok {
			out = append(out, h)
		}
	}
	return out
}
