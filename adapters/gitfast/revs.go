package gitfast

// revs.go — the revision expressions lore passes to git, resolved without
// a git process: a full object id, HEAD, a ref name (git's DWIM rules),
// with an optional ^{commit} / ^{tree} / ^{} peel or :path suffix. Every
// other expression (~, ^n, @{…}, abbreviated ids) answers ok=false.

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// result of a resolution: ok=false means "ask git"; found=false means git
// would report that the revision does not exist (or has the wrong type).
type resolved struct {
	id       string
	typ      string
	found    bool
	ok       bool
	treePath bool // the expression had a :path part
}

// notOK is the "ask git" answer.
var notOK = resolved{}

// notFound is a definite "does not exist" answer.
var notFound = resolved{ok: true}

// dwimRules are git's ref_rev_parse_rules after the bare name.
var dwimRules = []string{"refs/%s", "refs/tags/%s", "refs/heads/%s", "refs/remotes/%s", "refs/remotes/%s/HEAD"}

// resolveName turns a ref-ish name into an object id.
func (r *repo) resolveName(name string) (id string, found, ok bool) {
	if name == "" || strings.ContainsAny(name, "~^:@{}?*[\\ \t") || strings.Contains(name, "..") {
		return "", false, false
	}
	if name == "HEAD" || strings.HasPrefix(name, "refs/") {
		id, found, ok := r.resolveRef(name)
		if !ok || found || name == "HEAD" {
			return id, found, ok
		}
	} else if strings.ToUpper(name) == name {
		return "", false, false // FETCH_HEAD and other pseudo refs: ask git
	}
	for _, rule := range dwimRules {
		id, found, ok := r.resolveRef(strings.Replace(rule, "%s", name, 1))
		if !ok {
			return "", false, false
		}
		if found {
			return id, true, true
		}
	}
	return "", false, true
}

// resolve resolves a revision expression.
func (r *repo) resolve(expr string) resolved {
	if expr == "" || strings.HasPrefix(expr, "-") {
		return notOK
	}
	if strings.HasPrefix(expr, ":") {
		return r.indexLookup(expr)
	}
	base, path, hasPath := strings.Cut(expr, ":")
	if hasPath {
		if base == "" || strings.Contains(path, ":") || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") || strings.Contains(path, "//") || strings.HasSuffix(path, "/") {
			return notOK
		}
	}
	peel := ""
	if i := strings.Index(base, "^{"); i >= 0 && !strings.HasSuffix(base, "}") {
		return notOK
	}
	if i := strings.Index(base, "^{"); i >= 0 {
		if !strings.HasSuffix(base, "}") {
			return notOK
		}
		peel = base[i+2 : len(base)-1]
		base = base[:i]
		if peel != "commit" && peel != "tree" && peel != "" {
			return notOK
		}
	}
	base, back, ok := cutAncestry(base)
	if !ok {
		return notOK
	}
	var id string
	if isFullID(base) {
		id = base
	} else {
		rid, found, ok := r.resolveName(base)
		if !ok {
			return notOK
		}
		if !found {
			return notFound
		}
		id = rid
	}
	if peel == "" && !hasPath && back == 0 {
		// git prints a full id or a ref's value without looking the
		// object up (no peel asked)
		return resolved{id: id, found: true, ok: true}
	}
	objs, err := r.objects()
	if err != nil {
		return notOK
	}
	for range back {
		ps, ok := parents(objs, id)
		if !ok {
			return notOK // not a commit, or unreadable: git words it
		}
		if len(ps) == 0 {
			return notFound
		}
		id = ps[0]
	}
	o, err := objs.read(id)
	if errors.Is(err, errMissing) {
		return notFound
	}
	if err != nil {
		return notOK
	}
	typ := o.typ
	if peel != "" || hasPath {
		want := peel
		if hasPath {
			want = typeTree // x:path, x^{commit}:path and x^{tree}:path all read x's tree
		}
		id, typ, o, err = peelTo(objs, id, o, want)
		if errors.Is(err, errMissing) {
			return notFound
		}
		if err != nil {
			return notOK
		}
	}
	if !hasPath {
		return resolved{id: id, typ: typ, found: true, ok: true}
	}
	if path == "" {
		return resolved{id: id, typ: typeTree, found: true, ok: true, treePath: true}
	}
	eid, etyp, found, ok := lookupPath(objs, o.data, path)
	if !ok {
		return notOK
	}
	if !found {
		return resolved{ok: true, treePath: true}
	}
	return resolved{id: eid, typ: etyp, found: true, ok: true, treePath: true}
}

// peelTo follows tags (and commit -> tree) until an object of type want
// ("" = the first non-tag object).
func peelTo(objs *objectStore, id string, o object, want string) (string, string, object, error) {
	for range 64 {
		switch {
		case o.typ == want, want == "" && o.typ != typeTag:
			return id, o.typ, o, nil
		case o.typ == typeTag:
			next, ok := headerField(o.data, "object")
			if !ok {
				return "", "", object{}, errUnsupported
			}
			id = next
		case o.typ == typeCommit && want == typeTree:
			next, ok := headerField(o.data, "tree")
			if !ok {
				return "", "", object{}, errUnsupported
			}
			id = next
		default:
			return "", "", object{}, errMissing // git: "expected commit type"
		}
		var err error
		o, err = objs.read(id)
		if err != nil {
			return "", "", object{}, err
		}
	}
	return "", "", object{}, errUnsupported
}

// headerField returns the first "<key> <value>" header line of a commit or
// tag object.
func headerField(data []byte, key string) (string, bool) {
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		if len(line) == 0 {
			break
		}
		if v, ok := bytes.CutPrefix(line, []byte(key+" ")); ok {
			return string(v), true
		}
	}
	return "", false
}

// treeEntry is one entry of a tree object.
type treeEntry struct {
	mode string
	name string
	id   string
}

// typ is the object type the entry's mode names.
func (e treeEntry) typ() string {
	switch e.mode {
	case "40000":
		return typeTree
	case "160000":
		return typeCommit
	}
	return typeBlob
}

// parseTree splits a tree object into entries.
func parseTree(data []byte) ([]treeEntry, bool) {
	var out []treeEntry
	for len(data) > 0 {
		sp := bytes.IndexByte(data, ' ')
		if sp < 0 {
			return nil, false
		}
		nul := bytes.IndexByte(data[sp:], 0)
		if nul < 0 || sp+nul+21 > len(data) {
			return nil, false
		}
		nul += sp
		out = append(out, treeEntry{mode: string(data[:sp]), name: string(data[sp+1 : nul]), id: hex.EncodeToString(data[nul+1 : nul+21])})
		data = data[nul+21:]
	}
	return out, true
}

// lookupPath walks a slash path down from a tree.
func lookupPath(objs *objectStore, tree []byte, path string) (id, typ string, found, ok bool) {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		entries, pok := parseTree(tree)
		if !pok {
			return "", "", false, false
		}
		var hit *treeEntry
		for k := range entries {
			if entries[k].name == part {
				hit = &entries[k]
				break
			}
		}
		if hit == nil {
			return "", "", false, true
		}
		if i == len(parts)-1 {
			return hit.id, hit.typ(), true, true
		}
		if hit.typ() != typeTree {
			return "", "", false, true
		}
		o, err := objs.read(hit.id)
		if err != nil {
			return "", "", false, false
		}
		tree = o.data
	}
	return "", "", false, false
}

// cutAncestry strips trailing first-parent steps ("~", "~N", "^", "^1")
// and returns how many; ok=false for any other suffix (^2, ~^…{…}).
func cutAncestry(base string) (string, int, bool) {
	back := 0
	for {
		switch {
		case strings.HasSuffix(base, "^1"):
			base, back = base[:len(base)-2], back+1
		case strings.HasSuffix(base, "^"):
			base, back = base[:len(base)-1], back+1
		default:
			i := strings.LastIndexByte(base, '~')
			if i < 0 {
				return base, back, !strings.ContainsAny(base, "^~")
			}
			n := 1
			if digits := base[i+1:]; digits != "" {
				v, err := strconv.Atoi(digits)
				if err != nil || v < 0 || v > 10000 {
					return "", 0, false
				}
				n = v
			}
			base, back = base[:i], back+n
		}
	}
}
