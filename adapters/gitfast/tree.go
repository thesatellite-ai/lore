package gitfast

// tree.go — ls-tree and tree-to-tree diff without a git process.

import (
	"bytes"
	"strings"
)

// pathspec is a list of literal path prefixes (git's rule for ls-tree and
// diff with plain paths: the whole path or a prefix ending at a slash).
type pathspec []string

// parsePathspec checks the paths are plain (no glob characters unless
// --literal-pathspecs, no ./ or .. parts) and strips trailing slashes.
func parsePathspec(paths []string, literal bool) (pathspec, bool) {
	var out pathspec
	for _, p := range paths {
		if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, ":") || strings.Contains(p, "//") {
			return nil, false
		}
		if !literal && strings.ContainsAny(p, "*?[\\") {
			return nil, false
		}
		for c := range strings.SplitSeq(strings.TrimSuffix(p, "/"), "/") {
			if c == "." || c == ".." {
				return nil, false
			}
		}
		out = append(out, p)
	}
	return out, true
}

// matches reports whether path is selected (or, for a tree, whether
// anything under it may be).
func (ps pathspec) matches(path string, isTree bool) bool {
	if len(ps) == 0 {
		return true
	}
	for _, p := range ps {
		q := strings.TrimSuffix(p, "/")
		switch {
		case path == q:
			if strings.HasSuffix(p, "/") && !isTree {
				continue // "dir/" never names a file
			}
			return true
		case strings.HasPrefix(path, q+"/"):
			return true
		case isTree && strings.HasPrefix(q, path+"/"):
			return true
		}
	}
	return false
}

// needsQuote reports whether git would C-quote path in non -z output.
func needsQuote(path string) bool {
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c < 0x20 || c >= 0x7f || c == '"' || c == '\\' {
			return true
		}
	}
	return false
}

// lsTree answers `ls-tree [-r] [-z] [--name-only] [--full-tree] <rev> [--] [paths]`.
func lsTree(r *repo, inv invocation) (Reply, bool) {
	recursive, z, nameOnly, fullTree := false, false, false, false
	rev := ""
	var paths []string
	dashdash := false
	for _, a := range inv.args {
		switch {
		case dashdash:
			paths = append(paths, a)
		case a == "--":
			dashdash = true
		case a == "-r":
			recursive = true
		case a == "-z":
			z = true
		case a == "--name-only" || a == "--name-status":
			nameOnly = true
		case a == "--full-tree":
			fullTree = true
		case strings.HasPrefix(a, "-"):
			return Reply{}, false
		case rev == "":
			rev = a
		default:
			paths = append(paths, a)
		}
	}
	if rev == "" {
		return Reply{}, false
	}
	prefix, ok := r.prefixOf(inv.dir)
	if !ok || (prefix != "" && !fullTree) {
		return Reply{}, false // paths relative to a subfolder: git
	}
	ps, ok := parsePathspec(paths, inv.literal)
	if !ok {
		return Reply{}, false
	}
	if !recursive {
		for _, p := range ps {
			if !strings.HasSuffix(p, "/") {
				return Reply{}, false // a non-recursive "dir" names the entry: git
			}
		}
	}
	res := r.resolve(rev + "^{tree}")
	if !res.ok || !res.found {
		return Reply{}, false
	}
	objs, err := r.objects()
	if err != nil {
		return Reply{}, false
	}
	var out bytes.Buffer
	var walk func(id, base string) bool
	walk = func(id, base string) bool {
		o, err := objs.read(id)
		if err != nil || o.typ != typeTree {
			return false
		}
		entries, ok := parseTree(o.data)
		if !ok {
			return false
		}
		for _, e := range entries {
			path := base + e.name
			isTree := e.typ() == typeTree
			if recursive {
				if !ps.matches(path, isTree) {
					continue
				}
				if isTree {
					if !walk(e.id, path+"/") {
						return false
					}
					continue
				}
			} else if len(ps) > 0 && !listedChild(ps, path) {
				if isTree && hasDeeper(ps, path) {
					if !walk(e.id, path+"/") {
						return false
					}
				}
				continue
			}
			if !z && needsQuote(path) {
				return false
			}
			if !nameOnly {
				mode := e.mode
				if isTree {
					mode = "040000"
				}
				out.WriteString(mode + " " + e.typ() + " " + e.id + "\t")
			}
			out.WriteString(path)
			if z {
				out.WriteByte(0)
			} else {
				out.WriteByte('\n')
			}
		}
		return true
	}
	if !walk(res.id, "") {
		return Reply{}, false
	}
	return Reply{Out: out.Bytes()}, true
}

// hasDeeper reports whether a non-recursive "dir/" pathspec asks for the
// children of path or of something below it.
func hasDeeper(ps pathspec, path string) bool {
	for _, p := range ps {
		if strings.HasPrefix(p, path+"/") {
			return true
		}
	}
	return false
}

// listedChild reports whether path is a direct child of a "dir/" pathspec.
func listedChild(ps pathspec, path string) bool {
	for _, p := range ps {
		if strings.HasPrefix(path, p) && !strings.Contains(path[len(p):], "/") {
			return true
		}
	}
	return false
}

// diffTrees answers `diff --name-status|--name-only -z --no-renames <a> <b> [-- paths]`
// and the same with `diff-tree -r`.
func diffTrees(r *repo, inv invocation) (Reply, bool) {
	status, names, z, noRenames, recursive := false, false, false, false, false
	var revs, paths []string
	dashdash := false
	for _, a := range inv.args {
		switch {
		case dashdash:
			paths = append(paths, a)
		case a == "--":
			dashdash = true
		case a == "-r":
			recursive = true
		case a == "--name-status":
			status = true
		case a == "--name-only":
			names = true
		case a == "-z":
			z = true
		case a == "--no-renames":
			noRenames = true
		case strings.HasPrefix(a, "-"):
			return Reply{}, false
		default:
			revs = append(revs, a)
		}
	}
	if inv.sub == "diff-tree" {
		// plumbing: never detects renames, recursive only with -r
		if !recursive {
			return Reply{}, false
		}
		noRenames = true
	}
	if len(revs) != 2 || status == names || (!noRenames && status) {
		return Reply{}, false
	}
	prefix, ok := r.prefixOf(inv.dir)
	if !ok || prefix != "" {
		return Reply{}, false
	}
	ps, ok := parsePathspec(paths, inv.literal)
	if !ok {
		return Reply{}, false
	}
	a, b := r.resolve(revs[0]+"^{tree}"), r.resolve(revs[1]+"^{tree}")
	if !a.ok || !b.ok || !a.found || !b.found {
		return Reply{}, false
	}
	objs, err := r.objects()
	if err != nil {
		return Reply{}, false
	}
	var out bytes.Buffer
	emit := func(letter, path string) bool {
		if !z && needsQuote(path) {
			return false
		}
		sep := byte('\n')
		if z {
			sep = 0
		}
		if status {
			out.WriteString(letter)
			if z {
				out.WriteByte(0)
			} else {
				out.WriteByte('\t')
			}
		}
		out.WriteString(path)
		out.WriteByte(sep)
		return true
	}
	if a.id == b.id {
		return Reply{}, true // one tree: nothing changed, renames or not
	}
	if !noRenames {
		return Reply{}, false // rename detection (diff.renames): git decides
	}
	if !diffWalk(objs, a.id, b.id, "", ps, emit) {
		return Reply{}, false
	}
	return Reply{Out: out.Bytes()}, true
}

// sortKey is git's tree order key: trees sort as "name/".
func sortKey(e treeEntry) string {
	if e.typ() == typeTree {
		return e.name + "/"
	}
	return e.name
}

// diffWalk compares two trees ("" id = empty) and emits A/D/M/T per file.
func diffWalk(objs *objectStore, aID, bID, base string, ps pathspec, emit func(letter, path string) bool) bool {
	var ae, be []treeEntry
	for _, side := range []struct {
		id  string
		dst *[]treeEntry
	}{{aID, &ae}, {bID, &be}} {
		if side.id == "" {
			continue
		}
		o, err := objs.read(side.id)
		if err != nil || o.typ != typeTree {
			return false
		}
		es, ok := parseTree(o.data)
		if !ok {
			return false
		}
		*side.dst = es
	}
	i, j := 0, 0
	for i < len(ae) || j < len(be) {
		var x, y *treeEntry
		switch {
		case i >= len(ae):
			y = &be[j]
			j++
		case j >= len(be):
			x = &ae[i]
			i++
		default:
			ka, kb := sortKey(ae[i]), sortKey(be[j])
			switch {
			case ka < kb:
				x = &ae[i]
				i++
			case ka > kb:
				y = &be[j]
				j++
			default:
				x, y = &ae[i], &be[j]
				i++
				j++
			}
		}
		if !diffEntry(objs, x, y, base, ps, emit) {
			return false
		}
	}
	return true
}

// diffEntry handles one name present on one or both sides.
func diffEntry(objs *objectStore, x, y *treeEntry, base string, ps pathspec, emit func(letter, path string) bool) bool {
	e := x
	if e == nil {
		e = y
	}
	path := base + e.name
	isTree := e.typ() == typeTree
	if !ps.matches(path, isTree) {
		return true
	}
	switch {
	case x != nil && y != nil:
		if x.id == y.id && x.mode == y.mode {
			return true
		}
		if isTree {
			return diffWalk(objs, x.id, y.id, path+"/", ps, emit)
		}
		letter := "M"
		if fileKind(x.mode) != fileKind(y.mode) {
			letter = "T"
		}
		return emit(letter, path)
	case x != nil:
		if isTree {
			return diffWalk(objs, x.id, "", path+"/", ps, emit)
		}
		return emit("D", path)
	default:
		if isTree {
			return diffWalk(objs, "", y.id, path+"/", ps, emit)
		}
		return emit("A", path)
	}
}

// fileKind groups modes the way diff tells a type change: regular files
// (either permission), symlinks, submodules.
func fileKind(mode string) string {
	switch mode {
	case "100644", "100755", "100664":
		return "file"
	}
	return mode
}
