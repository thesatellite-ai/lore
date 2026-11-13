package wire

// rendered_files.go — the files `lore render` writes at a fixed place in
// a project and that the project commits. git merges each with the
// lore-keep-ours driver (wire/host .gitattributes block), and lore doctor
// counts a merge conflict left in one (devco D-19: .lore/LORE.full.md had
// no rule, and a conflict in it showed as "open conflicts: 0").

import (
	"path"
	"strings"
)

// RenderedFilePatterns are the project-relative gitattributes patterns of
// the committed rendered files: LORE.md, LORE.full.md and the per-tool
// path and folder rule files (lore-path-*, lore-dir-*). A `*` never crosses
// a `/`, as in .gitattributes, so path.Match checks the same set git does.
var RenderedFilePatterns = []string{
	".lore/LORE.md",
	".lore/LORE.full.md",
	".claude/rules/lore/lore-*.md",
	".cursor/rules/lore-*.mdc",
}

// IsRenderedFile reports whether rel (relative to the project root, either
// separator) is one of RenderedFilePatterns.
func IsRenderedFile(rel string) bool {
	rel = strings.ReplaceAll(rel, "\\", "/")
	for _, p := range RenderedFilePatterns {
		if ok, err := path.Match(p, rel); err == nil && ok {
			return true
		}
	}
	return false
}
