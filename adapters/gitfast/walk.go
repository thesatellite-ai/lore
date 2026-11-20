package gitfast

// walk.go — commit-graph questions (rev-list --count, a line of new
// commits with its boundary, merge-base) over
// full object ids, without a git process. Large histories fall back.

import (
	"bytes"
	"strconv"
	"strings"
)

// maxWalk bounds a history walk; a bigger graph is left to git (which has
// commit-graph files and date heuristics for it).
const maxWalk = 20000

// parents returns a commit's parent ids.
func parents(objs *objectStore, id string) ([]string, bool) {
	o, err := objs.read(id)
	if err != nil || o.typ != typeCommit {
		return nil, false
	}
	var ps []string
	for line := range bytes.SplitSeq(o.data, []byte{'\n'}) {
		if len(line) == 0 {
			break
		}
		if v, ok := bytes.CutPrefix(line, []byte("parent ")); ok {
			ps = append(ps, string(v))
		}
	}
	return ps, true
}

// ancestors returns every commit reachable from the starts (inclusive).
func ancestors(objs *objectStore, starts []string) (map[string]bool, bool) {
	seen := map[string]bool{}
	stack := append([]string(nil), starts...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		if len(seen) > maxWalk {
			return nil, false
		}
		ps, ok := parents(objs, id)
		if !ok {
			return nil, false
		}
		stack = append(stack, ps...)
	}
	return seen, true
}

// commitArg resolves a full id or ref to a commit id.
func commitArg(r *repo, a string) (string, bool) {
	res := r.resolve(a + "^{commit}")
	if !res.ok || !res.found {
		return "", false
	}
	return res.id, true
}

// revList answers `rev-list --count <a> ^<b>…`.
func revList(r *repo, args []string) (Reply, bool) {
	if len(args) < 2 || args[0] != "--count" {
		return Reply{}, false
	}
	var pos, neg []string
	for _, a := range args[1:] {
		n, isNeg := strings.CutPrefix(a, "^")
		if strings.HasPrefix(n, "-") || n == "" {
			return Reply{}, false
		}
		id, ok := commitArg(r, n)
		if !ok {
			return Reply{}, false
		}
		if isNeg {
			neg = append(neg, id)
		} else {
			pos = append(pos, id)
		}
	}
	objs, err := r.objects()
	if err != nil {
		return Reply{}, false
	}
	in, ok := ancestors(objs, pos)
	if !ok {
		return Reply{}, false
	}
	if len(neg) > 0 {
		out, ok := ancestors(objs, neg)
		if !ok {
			return Reply{}, false
		}
		for id := range out {
			delete(in, id)
		}
	}
	return Reply{Out: []byte(strconv.Itoa(len(in)) + "\n")}, true
}

// mergeBase answers `merge-base <a> <b>` (one best base only) and
// `merge-base --is-ancestor <a> <b>`.
func mergeBase(r *repo, args []string) (Reply, bool) {
	isAnc := false
	var revs []string
	for _, a := range args {
		switch {
		case a == "--is-ancestor":
			isAnc = true
		case strings.HasPrefix(a, "-"):
			return Reply{}, false
		default:
			revs = append(revs, a)
		}
	}
	if len(revs) != 2 {
		return Reply{}, false
	}
	a, ok1 := commitArg(r, revs[0])
	b, ok2 := commitArg(r, revs[1])
	if !ok1 || !ok2 {
		return Reply{}, false
	}
	objs, err := r.objects()
	if err != nil {
		return Reply{}, false
	}
	if isAnc {
		if a == b {
			return Reply{}, true
		}
		anc, ok := ancestors(objs, []string{b})
		if !ok {
			return Reply{}, false
		}
		if anc[a] {
			return Reply{}, true
		}
		return Reply{Code: 1}, true
	}
	if a == b {
		return Reply{Out: []byte(a + "\n")}, true
	}
	ancA, ok := ancestors(objs, []string{a})
	if !ok {
		return Reply{}, false
	}
	if ancA[b] {
		return Reply{Out: []byte(b + "\n")}, true
	}
	ancB, ok := ancestors(objs, []string{b})
	if !ok {
		return Reply{}, false
	}
	if ancB[a] {
		return Reply{Out: []byte(a + "\n")}, true
	}
	// common ancestors; the best are those no other common one reaches
	var common []string
	for id := range ancA {
		if ancB[id] {
			common = append(common, id)
		}
	}
	if len(common) == 0 {
		return Reply{Code: 1}, true
	}
	var below []string
	for _, c := range common {
		ps, ok := parents(objs, c)
		if !ok {
			return Reply{}, false
		}
		below = append(below, ps...)
	}
	reached, ok := ancestors(objs, below)
	if !ok {
		return Reply{}, false
	}
	var best []string
	for _, c := range common {
		if !reached[c] {
			best = append(best, c)
		}
	}
	if len(best) != 1 {
		return Reply{}, false // criss-cross: git picks by its own order
	}
	return Reply{Out: []byte(best[0] + "\n")}, true
}

// revListRange answers the commit listings the leak checks of the commit
// and push hooks ask (devco:D-780):
//
//	rev-list [--boundary] [--parents] [--no-merges] <tip> [^<rev>…] [--not (<rev>|--remotes[=<remote>])…]
//
// Only one tip whose new commits form a line (no merge among them) is
// answered: for a line git's order is fixed (the tip first, then each
// parent; the one boundary commit, prefixed "-", last), whatever the
// commit dates say. Everything else (several tips, a merge, a glob in
// --remotes=, any other flag) is left to git. Why: each of these ran one
// git process in every commit and push hook, over the hook budget of 5.
func revListRange(r *repo, args []string) (Reply, bool) {
	boundary, withParents, negated := false, false, false
	var tips, negs []string
	for _, a := range args {
		switch {
		case a == "--boundary":
			boundary = true
		case a == "--parents":
			withParents = true
		case a == "--no-merges":
			// a line has no merge to drop
		case a == "--not":
			negated = !negated
		case a == "--remotes" || strings.HasPrefix(a, "--remotes="):
			if !negated || a == "--remotes=" {
				return Reply{}, false // a positive --remotes lists several tips
			}
			ids, ok := remoteTips(r, strings.TrimPrefix(strings.TrimPrefix(a, "--remotes"), "="))
			if !ok {
				return Reply{}, false
			}
			negs = append(negs, ids...)
		case strings.HasPrefix(a, "-") || a == "":
			return Reply{}, false
		default:
			n, caret := strings.CutPrefix(a, "^")
			if n == "" || strings.HasPrefix(n, "-") {
				return Reply{}, false
			}
			id, ok := commitArg(r, n)
			if !ok {
				return Reply{}, false
			}
			if caret != negated {
				negs = append(negs, id)
			} else {
				tips = append(tips, id)
			}
		}
	}
	if len(tips) != 1 {
		return Reply{}, false
	}
	objs, err := r.objects()
	if err != nil {
		return Reply{}, false
	}
	have, ok := ancestors(objs, negs)
	if !ok {
		return Reply{}, false
	}
	var b strings.Builder
	line := func(prefix, id string, ps []string) {
		b.WriteString(prefix + id)
		if withParents {
			for _, p := range ps {
				b.WriteString(" " + p)
			}
		}
		b.WriteByte('\n')
	}
	for cur, n := tips[0], 0; !have[cur]; n++ {
		if n > maxWalk {
			return Reply{}, false
		}
		ps, ok := parents(objs, cur)
		if !ok || len(ps) > 1 {
			return Reply{}, false // a merge: git orders its sides by date
		}
		line("", cur, ps)
		if len(ps) == 0 {
			break // the root: no boundary
		}
		if have[ps[0]] && boundary {
			bps, ok := parents(objs, ps[0])
			if !ok {
				return Reply{}, false
			}
			line("-", ps[0], bps)
		}
		cur = ps[0]
	}
	return Reply{Out: []byte(b.String())}, true
}

// remoteTips are the commits of the refs `--remotes[=<remote>]` names:
// every refs/remotes/ ref, or those under refs/remotes/<remote>/. ok is
// false for a glob (git matches it its own way) or an unreadable ref.
func remoteTips(r *repo, remote string) ([]string, bool) {
	if strings.ContainsAny(remote, "*?[\\") {
		return nil, false
	}
	prefix := "refs/remotes/"
	if remote != "" {
		prefix += strings.TrimSuffix(remote, "/") + "/"
	}
	refs, ok := r.listRefs()
	if !ok {
		return nil, false
	}
	var ids []string
	for name, id := range refs {
		if strings.HasPrefix(name, prefix) {
			cid, ok := commitArg(r, id)
			if !ok {
				return nil, false
			}
			ids = append(ids, cid)
		}
	}
	return ids, true
}
