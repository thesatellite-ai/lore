package skill

// links.go — a tool's skill folder as it sits on disk, symbolic links
// followed. A shared-skills tool links ~/.claude/skills/lore to
// ~/.agents/skills/lore, itself a link to the real folder; filepath.WalkDir
// does not follow a linked root, so the folder read as empty and lore said
// "not installed" over a working install (owner, lore-dev 0.1.18).
//
//	locate(dir) ─▶ plain folder / missing ─▶ read dir itself
//	            └▶ link ─▶ follow every hop ─▶ end exists ─▶ read the end
//	                                         └▶ dangling ─▶ nothing to read
//	readTree(real) ─▶ every file, linked files and folders followed (loops cut)

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// maxLinkHops bounds a chain of links (the kernel's own limit is 40).
const maxLinkHops = 40

// gitDir is never read as part of a skill: a linked target that is a git
// checkout keeps its history there, and it is no bundle file.
const gitDir = ".git"

// place is one tool's skill folder: where it is named and where its files
// really are.
type place struct {
	// Dir is the folder under home (the profile's skill_dir).
	Dir string
	// Real is where the files are read: Dir itself, or the end of its link
	// chain with every link resolved.
	Real string
	// Link is the end of Dir's link chain; "" when Dir is no link.
	Link string
	// Dangling: Dir is a link whose end does not exist.
	Dangling bool
}

// linked reports whether Dir is a symbolic link (dangling or not).
func (p place) linked() bool { return p.Link != "" }

// locate finds where dir's files are. A missing dir is a plain empty
// place; a link is followed hop by hop so a dangling end is still named.
func locate(dir string) (place, error) {
	pl := place{Dir: dir, Real: dir}
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return pl, nil
	case err != nil:
		return pl, err
	case fi.Mode()&fs.ModeSymlink == 0:
		return pl, nil
	}
	end, dangling, err := linkEnd(dir)
	if err != nil {
		return pl, err
	}
	pl.Link, pl.Dangling = end, dangling
	if dangling {
		pl.Real = end
		return pl, nil
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return pl, err
	}
	pl.Real = real
	return pl, nil
}

// linkEnd follows the link at p hop by hop: the last path of the chain,
// and whether it does not exist (a dangling link). A chain longer than
// maxLinkHops is E_SYMLINK_LOOP.
func linkEnd(p string) (string, bool, error) {
	cur := p
	for range maxLinkHops {
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return cur, true, nil
		}
		if err != nil {
			return "", false, err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			return cur, false, nil
		}
		next, err := os.Readlink(cur)
		if err != nil {
			return "", false, err
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(cur), next)
		}
		cur = filepath.Clean(next)
	}
	return "", false, errs.New(errs.SymlinkLoop, fmt.Sprintf("%s: more than %d symbolic links in a row (a loop?)", p, maxLinkHops)).
		WithHint("fix or remove the link " + p + ", then run the command again")
}

// tree is a skill folder's content: every regular file by slash path, and
// every entry that is a symbolic link (file or folder) with its chain's end.
type tree struct {
	Files map[string][]byte
	Links map[string]string
}

// readTree reads every file under root, following linked files and linked
// folders (a folder seen twice, a loop among them, is read once). A missing
// root is an empty tree; .git folders are skipped.
func readTree(root string) (tree, error) {
	t := tree{Files: map[string][]byte{}, Links: map[string]string{}}
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	err := t.walk(root, "", map[string]bool{})
	return t, err
}

// walk reads dir into t under the slash prefix.
func (t tree) walk(dir, prefix string, seen map[string]bool) error {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if seen[real] {
		return nil // a linked folder already read: a loop or a second link to it
	}
	seen[real] = true
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.Name() == gitDir {
			continue
		}
		p := filepath.Join(dir, e.Name())
		rel := path.Join(prefix, e.Name())
		typ := e.Type()
		if typ&fs.ModeSymlink != 0 {
			end, dangling, lerr := linkEnd(p)
			if lerr != nil {
				return lerr
			}
			t.Links[rel] = end
			if dangling {
				continue // nothing to read; status names it through the missing file
			}
			st, serr := os.Stat(p)
			if serr != nil {
				return serr
			}
			typ = st.Mode().Type()
		}
		switch {
		case typ.IsDir():
			if err := t.walk(p, rel, seen); err != nil {
				return err
			}
		case typ.IsRegular():
			b, rerr := os.ReadFile(filepath.Clean(p))
			if rerr != nil {
				return rerr
			}
			t.Files[rel] = b
		}
	}
	return nil
}

// linkedBundleFiles lists the bundle files (keys of want) that are links,
// or sit under a linked folder, inside the skill folder; sorted.
func (t tree) linkedBundleFiles(want map[string][]byte) []string {
	var out []string
	for rel := range want {
		for l := range t.Links {
			if rel == l || strings.HasPrefix(rel, l+"/") {
				out = append(out, rel)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}

// isLoreSkill reports whether a SKILL.md is lore's: its frontmatter names
// it lore (`name: lore`). A foreign skill linked in under lore's folder
// name is never overwritten as if it were an old lore install.
func isLoreSkill(entry []byte) bool {
	lines := bytes.Split(entry, []byte("\n"))
	if len(lines) == 0 || string(bytes.TrimSpace(lines[0])) != "---" {
		return false
	}
	for _, l := range lines[1:] {
		s := bytes.TrimSpace(l)
		if string(s) == "---" {
			return false
		}
		if k, v, ok := bytes.Cut(s, []byte(":")); ok && string(bytes.TrimSpace(k)) == "name" {
			return string(bytes.Trim(bytes.TrimSpace(v), `"'`)) == "lore"
		}
	}
	return false
}
