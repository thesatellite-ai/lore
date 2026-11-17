package renderhost

// context_mention.go — when a rule's body names a file (context --for-file
// step 3). Why not strings.Contains: `--for-file main.go` matched a rule
// about another repo's saas/cmd/cron/main.go, because "main.go" is a
// substring of that path (devco D-574). A mention is the whole repo-relative
// path standing on its own: not the tail of a longer path, not the head of
// a longer name.

import "strings"

// mentionsPath reports whether body names p as a whole path: the
// character before each occurrence is not part of a path (so
// "cron/main.go" does not mention "main.go"), and the one after does not
// continue the name ("main.gox", "main.go/x", "main.go.bak"; a sentence's
// full stop is fine). A p ending in "/" (a folder) only needs the left edge.
func mentionsPath(body, p string) bool {
	if p == "" {
		return false
	}
	for from := 0; from <= len(body)-len(p); {
		i := strings.Index(body[from:], p)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(p)
		from = start + 1
		if start > 0 && isPathByte(body[start-1]) {
			continue
		}
		if strings.HasSuffix(p, "/") || end == len(body) {
			return true
		}
		next := body[end]
		if next == '.' {
			if end+1 < len(body) && isPathByte(body[end+1]) {
				continue // main.go.bak
			}
			return true
		}
		if !isPathByte(next) {
			return true
		}
	}
	return false
}

// isPathByte is a byte that can sit inside a file path.
func isPathByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '/', c == '.', c == '_', c == '-', c == '~', c == '\\':
		return true
	}
	return c >= 0x80 // a non-ASCII letter of a file name
}
