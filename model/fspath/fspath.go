// Package fspath is lore's one rule for comparing and storing folder and
// file paths.
//
// Why it exists: the same folder can be named by two different strings.
// On macOS every temp folder lives under /var, a symlink to /private/var:
// t.TempDir, $TMPDIR and a person's cwd say /var/folders/..., while git
// rev-parse, os.Getwd after a chdir and filepath.EvalSymlinks say
// /private/var/folders/.... A plain ==, strings.HasPrefix, filepath.Rel or
// a map keyed by path then sees two folders where there is one: a store
// read as outside itself, a repo walk skipped a linked repo, a lock looked
// like another checkout's (devco D-413, Mac findings M-6..M-12). Each
// caller used to resolve symlinks its own way (or not at all); this
// package is the single rule they all route through:
//
//	Canonical(p) ─▶ absolute, symlinks of the longest existing prefix
//	                resolved, the missing rest appended, Clean
//	Same(a, b)   ─▶ Canonical(a) == Canonical(b)
//	Within(p, r) ─▶ p is r or below r, both canonical
//	Rel(r, p)    ─▶ filepath.Rel of the canonical forms, ok=false outside
//	InTyped(t,c) ─▶ the folder c spelled the way the person typed t
//
// Store a path in its canonical form when it will be compared later
// (records, map keys, lock owners); compare with Same / Within / Rel. Never
// rewrite a path the person typed in output: print what they gave, or a
// derived folder in their spelling (InTyped).
//
// It only reads the filesystem (lstat and readlink through EvalSymlinks);
// it never fails: a path that cannot be made absolute is only Cleaned.
package fspath

import (
	"path/filepath"
	"strings"
)

// Canonical is p as one fixed string per folder: absolute, the symlinks of
// its longest existing prefix resolved (so a path that does not exist yet,
// such as a lock or a clone about to be made, still canonicalizes the same
// as it will once it exists), the rest appended, cleaned. "" stays "".
func Canonical(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	// walk up until a prefix resolves; rest holds the missing tail
	head, rest := abs, ""
	for {
		if real, rerr := filepath.EvalSymlinks(head); rerr == nil {
			if rest == "" {
				return filepath.Clean(real)
			}
			return filepath.Clean(filepath.Join(real, rest))
		}
		parent := filepath.Dir(head)
		if parent == head {
			return abs // nothing resolves (no such volume): abs is Clean already
		}
		base := filepath.Base(head)
		if rest == "" {
			rest = base
		} else {
			rest = filepath.Join(base, rest)
		}
		head = parent
	}
}

// Same reports whether a and b name the same path once both are
// canonical. Two empty paths are the same; an empty and a set one are not.
func Same(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	return a == b || Canonical(a) == Canonical(b)
}

// Rel is filepath.Rel(root, p) on the canonical forms, and whether p is
// root or below it (ok false when p is outside root or either is empty).
// rel is "." for root itself, slash-free of any leading "..".
func Rel(root, p string) (rel string, ok bool) {
	if root == "" || p == "" {
		return "", false
	}
	r, err := filepath.Rel(Canonical(root), Canonical(p))
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", false
	}
	return r, true
}

// Within reports whether p is root or a path below it, both canonical.
func Within(p, root string) bool {
	_, ok := Rel(root, p)
	return ok
}

// InTyped is the folder canonical (a canonical path lore found, such as a
// git top) spelled the way the person typed typed (a path at or below it):
// the nearest ancestor of typed, typed itself included, that is the same
// folder. When typed is not at or below canonical it returns canonical
// unchanged. Use it to echo a derived folder in the person's own spelling
// (/var/... on macOS stays /var/..., never /private/var/...).
func InTyped(typed, canonical string) string {
	if typed == "" || canonical == "" {
		return canonical
	}
	abs, err := filepath.Abs(typed)
	if err != nil {
		return canonical
	}
	want := Canonical(canonical)
	for d := abs; ; d = filepath.Dir(d) {
		if Canonical(d) == want {
			return d
		}
		if filepath.Dir(d) == d {
			return canonical
		}
	}
}
