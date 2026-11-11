package onboard

// folders.go — which setup a folder belongs to (ONBOARDING_DESIGN 3.3,
// step 3 "folder match"), and the two machine checks built on the same
// rules: folders.overlap (6.1, error) and folders.shadow (4.9, warn).
//
// Pure: no file system. Symlinks are resolved through the Resolver the
// caller passes (wire passes filepath.EvalSymlinks), so a test can name any
// layout without touching a disk.
//
//	path ─▶ resolve ─▶ every setup's globs (~ expanded, literal prefix
//	                   resolved) ─▶ the matching globs ─▶ highest
//	                   specificity ─▶ one setup: Found
//	                                  two setups: Tie (E_SETUP_AMBIGUOUS)

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// globAnyDepth is the segment matching zero or more path segments (3.2).
const globAnyDepth = "**"

// globMeta are the characters that make a segment a wildcard (path.Match).
const globMeta = "*?["

// pathSep is the separator globs are written with; folders are absolute
// slash paths on every platform lore matches setups on.
const pathSep = "/"

// Resolver resolves symlinks in an absolute path (filepath.EvalSymlinks).
// An error means "use the path as written" (a folder that does not exist
// yet still matches by its spelling). nil resolves nothing.
type Resolver func(p string) (string, error)

// FolderMatch is one glob of one setup that matches a folder.
type FolderMatch struct {
	// Setup is the setup's name.
	Setup string `json:"setup"`
	// Glob is the glob as written in config.yaml (with its ~).
	Glob string `json:"glob"`
	// Specificity is the number of literal segments before the first
	// wildcard, counted on the expanded, resolved glob (3.3 "most
	// specific"); only comparisons between globs matter.
	Specificity int `json:"specificity"`
}

// FolderResult is the folder step of 3.3.
type FolderResult struct {
	// Match is the winning glob; meaningful only when Found.
	Match FolderMatch
	// Found: exactly one setup holds the most specific matching glob.
	Found bool
	// Tie lists the most specific matches when two or more setups hold one
	// (sorted by setup name): E_SETUP_AMBIGUOUS, never a guess.
	Tie []FolderMatch
}

// Ambiguous reports a tie between setups.
func (r FolderResult) Ambiguous() bool { return len(r.Tie) > 1 }

// MatchFolder runs 3.3 step 3 for the folder dir (the git toplevel, else the
// current directory): the most specific glob wins; globs of one setup never
// tie with each other.
func MatchFolder(setups []SetupEntry, dir, home string, resolve Resolver) FolderResult {
	target := resolvePath(cleanSlash(ExpandHome(dir, home)), resolve)
	best := -1
	var hits []FolderMatch
	for _, s := range setups {
		bestOfSetup := -1
		var m FolderMatch
		for _, g := range s.Folders {
			cg := compileGlob(g, home, resolve)
			if !cg.match(target) || cg.spec <= bestOfSetup {
				continue
			}
			bestOfSetup, m = cg.spec, FolderMatch{Setup: s.Name, Glob: g, Specificity: cg.spec}
		}
		switch {
		case bestOfSetup < 0 || bestOfSetup < best:
		case bestOfSetup > best:
			best, hits = bestOfSetup, []FolderMatch{m}
		default:
			hits = append(hits, m)
		}
	}
	switch len(hits) {
	case 0:
		return FolderResult{}
	case 1:
		return FolderResult{Match: hits[0], Found: true}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Setup < hits[j].Setup })
	return FolderResult{Tie: hits}
}

// GlobMatch reports whether the absolute glob (already ~-expanded) matches
// the absolute path p: `**` is any depth (also zero segments), every other
// segment is path.Match. No symlink resolution; MatchFolder adds it.
func GlobMatch(glob, p string) bool {
	return matchSegs(splitSlash(cleanSlash(glob)), splitSlash(cleanSlash(p)))
}

// Specificity is the number of literal segments before the first wildcard
// of the absolute glob (3.3: `/w/projects/clients/acme/**` beats
// `/w/projects/**`).
func Specificity(glob string) int {
	n := 0
	for _, s := range splitSlash(cleanSlash(glob)) {
		if strings.ContainsAny(s, globMeta) {
			break
		}
		n++
	}
	return n
}

// Overlap is two setups' globs that tie on some folder (folders.overlap).
type Overlap struct {
	A, B FolderMatch
}

// Overlaps lists every pair of globs of different setups with the same
// specificity and the same literal prefix whose next segments can match a
// common name: some folder would match both equally, which 3.3 refuses
// with E_SETUP_AMBIGUOUS (checked ahead of time by folders.overlap).
func Overlaps(setups []SetupEntry, home string, resolve Resolver) []Overlap {
	type item struct {
		m  FolderMatch
		cg compiled
	}
	var all []item
	for _, s := range setups {
		for _, g := range s.Folders {
			cg := compileGlob(g, home, resolve)
			all = append(all, item{FolderMatch{Setup: s.Name, Glob: g, Specificity: cg.spec}, cg})
		}
	}
	var out []Overlap
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			a, b := all[i], all[j]
			if a.m.Setup == b.m.Setup || a.cg.spec != b.cg.spec || a.cg.prefix() != b.cg.prefix() {
				continue
			}
			if restsMeet(a.cg.rest(), b.cg.rest()) {
				out = append(out, Overlap{A: a.m, B: b.m})
			}
		}
	}
	return out
}

// Shadow is a glob of one setup that takes part of another setup's glob
// (folders.shadow, 4.9): "~/www/projects/clients/acme/** stays with
// client-acme".
type Shadow struct {
	// Shadowed is the broader glob (the setup being checked).
	Shadowed FolderMatch
	// By is the more specific glob of another setup.
	By FolderMatch
}

// Shadows lists, for the setup named name, every more specific glob of
// another setup whose literal folder lies inside one of name's globs.
func Shadows(setups []SetupEntry, name, home string, resolve Resolver) []Shadow {
	var mine []FolderMatch
	var mineC []compiled
	for _, s := range setups {
		if s.Name != name {
			continue
		}
		for _, g := range s.Folders {
			cg := compileGlob(g, home, resolve)
			mine, mineC = append(mine, FolderMatch{Setup: s.Name, Glob: g, Specificity: cg.spec}), append(mineC, cg)
		}
	}
	var out []Shadow
	for _, s := range setups {
		if s.Name == name {
			continue
		}
		for _, g := range s.Folders {
			cg := compileGlob(g, home, resolve)
			for i, m := range mineC {
				if cg.spec > m.spec && m.match(cg.prefix()) {
					out = append(out, Shadow{Shadowed: mine[i], By: FolderMatch{Setup: s.Name, Glob: g, Specificity: cg.spec}})
				}
			}
		}
	}
	return out
}

// LiteralPrefix is the absolute folder before the glob's first wildcard,
// ~ expanded (folders.exist checks it is a folder).
func LiteralPrefix(glob, home string) string {
	segs := splitSlash(cleanSlash(ExpandHome(glob, home)))
	n := Specificity(ExpandHome(glob, home))
	return joinAbs(segs[:n])
}

// compiled is a glob split into segments, its literal prefix resolved.
type compiled struct {
	segs []string
	spec int
}

func (c compiled) match(p string) bool { return matchSegs(c.segs, splitSlash(p)) }

func (c compiled) prefix() string { return joinAbs(c.segs[:c.spec]) }

func (c compiled) rest() []string { return c.segs[c.spec:] }

// compileGlob expands ~, cleans the glob and resolves its literal prefix
// (3.3: both sides compared after EvalSymlinks).
func compileGlob(glob, home string, resolve Resolver) compiled {
	abs := cleanSlash(ExpandHome(glob, home))
	n := Specificity(abs)
	segs := splitSlash(abs)
	lit := resolvePath(joinAbs(segs[:n]), resolve)
	out := append(splitSlash(lit), segs[n:]...)
	return compiled{segs: out, spec: len(splitSlash(lit))}
}

// restsMeet reports whether the wildcard parts of two globs sharing a
// literal prefix can match one common folder (conservative: a `**`, a `*`
// or equal text meet; two different patterns are assumed apart).
func restsMeet(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b) || (len(a) > 0 && a[0] == globAnyDepth) || (len(b) > 0 && b[0] == globAnyDepth)
	}
	x, y := a[0], b[0]
	return x == globAnyDepth || y == globAnyDepth || x == "*" || y == "*" || x == y
}

// matchSegs matches glob segments against path segments.
func matchSegs(g, p []string) bool {
	if len(g) == 0 {
		return len(p) == 0
	}
	if g[0] == globAnyDepth {
		for i := 0; i <= len(p); i++ {
			if matchSegs(g[1:], p[i:]) {
				return true
			}
		}
		return false
	}
	if len(p) == 0 {
		return false
	}
	ok, err := path.Match(g[0], p[0])
	if err != nil || !ok {
		return false
	}
	return matchSegs(g[1:], p[1:])
}

// resolvePath resolves p with resolve, keeping p when it fails.
func resolvePath(p string, resolve Resolver) string {
	if resolve == nil {
		return p
	}
	r, err := resolve(p)
	if err != nil || r == "" {
		return p
	}
	return cleanSlash(r)
}

// cleanSlash cleans p as a slash path (Windows separators turned to /).
func cleanSlash(p string) string { return path.Clean(filepath.ToSlash(p)) }

// joinAbs is the absolute slash path of segs: "/a/b", or "C:/a/b" when the
// first segment is a Windows drive. A "/" before the drive ("/C:/a") is no
// path Windows can resolve: the glob's literal prefix then stayed
// unresolved (C:/Users/RUNNER~1) while the folder resolved to its long
// name (C:/Users/runneradmin), and no setup matched.
func joinAbs(segs []string) string {
	if len(segs) > 0 && isDrive(segs[0]) {
		return strings.Join(segs, pathSep)
	}
	return pathSep + strings.Join(segs, pathSep)
}

// isDrive reports whether seg is a Windows drive ("C:").
func isDrive(seg string) bool {
	return len(seg) == 2 && seg[1] == ':' && ('a' <= seg[0]|0x20 && seg[0]|0x20 <= 'z')
}

// splitSlash splits a cleaned absolute slash path into its segments.
func splitSlash(p string) []string {
	p = strings.Trim(p, pathSep)
	if p == "" {
		return nil
	}
	return strings.Split(p, pathSep)
}
