// Package skills carries the agent skill bundle inside the binary, so
// `lore skill install` writes exactly the files this release was built
// with, on a machine that has no checkout and no network (ONBOARDING_DESIGN
// 4.7.1 "The version line": the bundle is embedded with go:embed).
//
// The bundle is what install.sh copies into ~/.claude/skills/lore: every
// markdown page at the top of this folder plus examples/. The source
// SKILL.md carries `lore_version: dev`; the installer replaces that one
// line with the binary's version (logic/toolprofile StampVersion), so a
// source checkout and an installed copy are told apart.
//
// Layer: stdlib only (embed, io/fs), so any layer may import it.
package skills

import (
	"embed"
	"io/fs"
	"sort"
)

// EntryFile is the bundle's entry page: the one that carries the frontmatter
// and the lore_version line.
const EntryFile = "SKILL.md"

// bundle is the embedded skill bundle. The patterns mirror install.sh's copy
// of skills/ (every page and the examples), minus this Go file.
//
//go:embed *.md examples/*.md
var bundle embed.FS

// FS returns the bundle as a read-only file system rooted at the bundle
// folder (paths like "SKILL.md", "examples/01-bootstrap.md").
func FS() fs.FS { return bundle }

// Files returns every bundle file keyed by its slash path relative to the
// bundle folder, plus the sorted list of those paths (a stable order for
// writes and reports). The SKILL.md bytes are the source ones, with
// `lore_version: dev`.
func Files() (map[string][]byte, []string, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(bundle, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := fs.ReadFile(bundle, p)
		if rerr != nil {
			return rerr
		}
		out[p] = b
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(out))
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	return out, names, nil
}
