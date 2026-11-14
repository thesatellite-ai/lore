package wire

// gitattr_override.go — devco:Q-11: in .gitattributes a later line wins
// for the same attribute, so a team's catch-all appended after lore's
// block (`* text=auto eol=crlf`, common for Windows teams) turned every row
// file CRLF: lore's `eol=lf` no longer applied and nothing said so. The
// block writer (wire/host) moves lore's block after such a line, and
// lore doctor names the line until it does.

import (
	"path"
	"strings"
)

// attrOverrideSamples are paths that lore's .gitattributes block covers:
// a row file in a table folder, a row file at the top of .lore/data, and
// the rendered files.
var attrOverrideSamples = append([]string{".lore/data/memories/mem_x.json", ".lore/data/_meta.json"}, renderedSamples()...)

// renderedSamples are RenderedFilePatterns with each `*` filled in.
func renderedSamples() []string {
	out := make([]string, 0, len(RenderedFilePatterns))
	for _, p := range RenderedFilePatterns {
		out = append(out, strings.ReplaceAll(p, "*", "x"))
	}
	return out
}

// attrOverrideKeys are the attributes lore's block sets that a later
// line can take back (`binary` is the macro -text -diff -merge).
var attrOverrideKeys = []string{"text", "eol", "merge", "binary"}

// AttrBlockOverride is the first line after lore's block (between
// "# >>> marker >>>" and "# <<< marker <<<") in the .gitattributes text
// content that sets text, eol, merge or binary for a path the block
// covers, so it overrides lore's rule there; ok false when there is none
// or no block. CRLF line ends are read as LF.
func AttrBlockOverride(content, marker string) (line string, ok bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	end := "# <<< " + marker + " <<<"
	i := strings.Index(content, "# >>> "+marker+" >>>")
	if i < 0 {
		return "", false
	}
	j := strings.Index(content[i:], end)
	if j < 0 {
		return "", false
	}
	for l := range strings.SplitSeq(content[i+j+len(end):], "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Fields(t)
		if len(f) < 2 || !setsAttrKey(f[1:]) {
			continue
		}
		for _, s := range attrOverrideSamples {
			if attrPatternMatches(f[0], s) {
				return t, true
			}
		}
	}
	return "", false
}

// setsAttrKey reports whether any of the attribute settings (`text`,
// `-text`, `!eol`, `eol=crlf`, ...) names one of attrOverrideKeys.
func setsAttrKey(attrs []string) bool {
	for _, a := range attrs {
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-!"), "=")
		for _, k := range attrOverrideKeys {
			if name == k {
				return true
			}
		}
	}
	return false
}

// attrPatternMatches is a gitattributes pattern test, close enough for
// the paths lore covers: a pattern with no `/` matches the base name at
// any depth; one with a `/` matches from the top, where `**` spans any
// number of folders. A pattern it cannot read counts as no match.
func attrPatternMatches(pattern, rel string) bool {
	pattern = strings.TrimPrefix(pattern, "/")
	if !strings.Contains(pattern, "/") {
		ok, err := path.Match(pattern, path.Base(rel))
		return err == nil && ok
	}
	return globDoubleStar(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

// globDoubleStar matches path segments against pattern segments, where a
// "**" segment matches zero or more segments (and a trailing "**" all
// that is left).
func globDoubleStar(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for k := 0; k <= len(segs); k++ {
				if globDoubleStar(pat[1:], segs[k:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
