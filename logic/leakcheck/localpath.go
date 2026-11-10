package leakcheck

// localpath.go — a known tool session id that is a folder or file name in
// a local path is not a leak of the session id (decided 2026-11-08).
//
// Why: tools name folders after their session ids (an agent's scratchpad,
// a transcript folder), so a repo created or cloned under such a folder
// has an origin like /home/pat/scratch/3f2a9c1e-…/lib. `lore init` then
// refused that origin as "looks like a secret (session-id)". The rule keeps
// catching the id where it is a value: in prose, an https url, a query
// string, a key=value line.

import "strings"

// tokenStops end the token a hit lies in (whitespace, quotes, brackets and
// the separators of key=value and lists).
const tokenStops = " \t\r\n\"'`<>()[]{},;="

// fileURLPrefix is the one url scheme that names a local path.
const fileURLPrefix = "file://"

// LocalPathSegment reports whether text[off:off+n] is a whole folder or
// file name of a local filesystem path: the token holding it starts with
// /, ~/, ./, ../, a drive letter (C:\ or C:/) or file://, and the hit sits
// between path separators (a file name may carry an extension after it).
func LocalPathSegment(text string, off, n int) bool {
	if off < 0 || n <= 0 || off+n > len(text) {
		return false
	}
	start := strings.LastIndexAny(text[:off], tokenStops) + 1
	end := len(text)
	if i := strings.IndexAny(text[off+n:], tokenStops); i >= 0 {
		end = off + n + i
	}
	token := text[start:end]
	if !localPathToken(token) {
		return false
	}
	if off == start || !isPathSep(text[off-1]) {
		return false
	}
	after := off + n
	return after == end || isPathSep(text[after]) || text[after] == '.'
}

func isPathSep(c byte) bool { return c == '/' || c == '\\' }

// localPathToken reports whether token is written as a local path.
func localPathToken(t string) bool {
	switch {
	case strings.HasPrefix(t, fileURLPrefix):
		return true
	case strings.Contains(t, "://"):
		return false // another scheme: a url, not a path
	case strings.HasPrefix(t, "/"), strings.HasPrefix(t, "~/"), strings.HasPrefix(t, "./"), strings.HasPrefix(t, "../"):
		return true
	case len(t) >= 3 && t[1] == ':' && isPathSep(t[2]) && (t[0]|0x20) >= 'a' && (t[0]|0x20) <= 'z':
		return true
	}
	return false
}
