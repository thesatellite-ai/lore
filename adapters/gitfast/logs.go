package gitfast

// logs.go — the one-commit log, unpushed-commit and staged-change
// questions the hooks and doctor ask on every run, without a git process:
//
//	log -1 --format=<F> <rev> [--]       one commit, a few atoms
//	log --format=<F> <rev> [--]          every commit of a line (no merge)
//	rev-list -1 <rev> --not --remotes    is <rev> unpushed (its id) or not ("")
//	diff --cached --quiet <rev> [-- p]   does the index differ from <rev>'s tree

import (
	"bytes"
	"strconv"
	"strings"
)

// logOne answers `log -1 --format=<F> <rev> [--]` (also -n1, -n 1,
// --max-count=1) for the atoms %H %T %P %an %ae %cn %ce %ct %at %B %n %%
// and %xNN. Without -1 it answers the whole history of rev when that
// history is a line (no merge, at most maxWalk commits): git's order is
// fixed then (rev first, then each parent), whatever the commit dates
// say; a merge, a missing parent (shallow) or a longer history is left to
// git. Why: doctor's agent_commits check (devco:D-983) reads every commit
// of lore's own refs, and a process per ref was over doctor's git budget.
// The Session has already refused configs that change log output
// (log.showSignature, i18n.logOutputEncoding).
func logOne(r *repo, args []string) (Reply, bool) {
	one, sep := false, false
	format := ""
	rev := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-1" || a == "-n1" || a == "--max-count=1":
			one = true
		case a == "-n" && i+1 < len(args) && args[i+1] == "1":
			one = true
			i++
		case strings.HasPrefix(a, "--format="):
			format = strings.TrimPrefix(a, "--format=")
		case strings.HasPrefix(a, "--pretty=format:"), strings.HasPrefix(a, "--pretty=tformat:"):
			format = a[strings.Index(a, ":")+1:]
			sep = strings.HasPrefix(a, "--pretty=format:") // separator, not terminator, between commits
		case a == "--":
			if i != len(args)-1 {
				return Reply{}, false // paths: git
			}
		case strings.HasPrefix(a, "-"):
			return Reply{}, false
		case rev == "":
			rev = a
		default:
			return Reply{}, false
		}
	}
	if format == "" || rev == "" || (!one && sep) {
		return Reply{}, false
	}
	res := r.resolve(rev + "^{commit}")
	if !res.ok || !res.found {
		return Reply{}, false
	}
	objs, err := r.objects()
	if err != nil {
		return Reply{}, false
	}
	var out bytes.Buffer
	for id, n := res.id, 0; id != ""; n++ {
		if n > maxWalk {
			return Reply{}, false
		}
		o, err := objs.read(id)
		if err != nil || o.typ != typeCommit {
			return Reply{}, false
		}
		c, ok := parseCommit(id, o.data)
		if !ok || (!one && len(c.parents) > 1) {
			return Reply{}, false // a merge: git orders its sides by date
		}
		if !formatCommit(&out, format, c) {
			return Reply{}, false
		}
		id = ""
		if !one && len(c.parents) == 1 {
			id = c.parents[0]
		}
	}
	return Reply{Out: out.Bytes()}, true
}

// formatCommit writes c in format (the atoms logOne lists) and git's
// terminating newline; false for an atom it does not know.
func formatCommit(out *bytes.Buffer, format string, c commitInfo) bool {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			out.WriteByte(format[i])
			continue
		}
		rest := format[i+1:]
		switch {
		case strings.HasPrefix(rest, "%"):
			out.WriteByte('%')
			i++
		case strings.HasPrefix(rest, "n"):
			out.WriteByte('\n')
			i++
		case strings.HasPrefix(rest, "H"):
			out.WriteString(c.id)
			i++
		case strings.HasPrefix(rest, "T"):
			out.WriteString(c.tree)
			i++
		case strings.HasPrefix(rest, "P"):
			out.WriteString(strings.Join(c.parents, " "))
			i++
		case strings.HasPrefix(rest, "B"):
			out.WriteString(c.message)
			i++
		case len(rest) >= 2 && (rest[0] == 'a' || rest[0] == 'c') && strings.IndexByte("net", rest[1]) >= 0:
			p := c.author
			if rest[0] == 'c' {
				p = c.committer
			}
			switch rest[1] {
			case 'n':
				out.WriteString(p.name)
			case 'e':
				out.WriteString(p.email)
			case 't':
				out.WriteString(p.when)
			}
			i += 2
		case len(rest) >= 3 && rest[0] == 'x':
			v, err := strconv.ParseUint(rest[1:3], 16, 8)
			if err != nil {
				return false
			}
			out.WriteByte(byte(v))
			i += 3
		default:
			return false
		}
	}
	out.WriteByte('\n')
	return true
}

// person is an author or committer line.
type person struct {
	name, email, when string
}

// commitInfo is the parsed commit header and message.
type commitInfo struct {
	id, tree          string
	parents           []string
	author, committer person
	message           string
}

// parseCommit splits a commit object. ok=false for an encoding header or
// an empty message. A message without a final newline (lore's own ref
// commits) is fine: git prints a user format's %B verbatim and adds no
// newline of its own (devco:D-983).
func parseCommit(id string, data []byte) (commitInfo, bool) {
	c := commitInfo{id: id}
	head, msg, found := bytes.Cut(data, []byte("\n\n"))
	if !found {
		return c, false
	}
	for line := range bytes.SplitSeq(head, []byte{'\n'}) {
		k, v, _ := bytes.Cut(line, []byte{' '})
		switch string(k) {
		case "tree":
			c.tree = string(v)
		case "parent":
			c.parents = append(c.parents, string(v))
		case "author", "committer":
			p, ok := parsePerson(string(v))
			if !ok {
				return c, false
			}
			if string(k) == "author" {
				c.author = p
			} else {
				c.committer = p
			}
		case "encoding":
			return c, false
		}
	}
	if len(msg) == 0 {
		return c, false
	}
	c.message = string(msg)
	return c, c.tree != ""
}

// parsePerson parses "Name <email> 1700000000 +0000".
func parsePerson(v string) (person, bool) {
	lt := strings.IndexByte(v, '<')
	gt := strings.LastIndexByte(v, '>')
	if lt < 0 || gt < lt {
		return person{}, false
	}
	fields := strings.Fields(v[gt+1:])
	if len(fields) != 2 {
		return person{}, false
	}
	return person{name: strings.TrimSpace(v[:lt]), email: v[lt+1 : gt], when: fields[0]}, true
}

// revListUnpushed answers `rev-list -1 <rev> --not --remotes`: rev's id
// when no remote-tracking ref reaches it, else nothing.
func revListUnpushed(r *repo, args []string) (Reply, bool) {
	if len(args) != 4 || args[0] != "-1" || args[2] != "--not" || args[3] != "--remotes" {
		return Reply{}, false
	}
	tip, ok := commitArg(r, args[1])
	if !ok {
		return Reply{}, false
	}
	refs, ok := r.listRefs()
	if !ok {
		return Reply{}, false
	}
	var remotes []string
	for name, id := range refs {
		if strings.HasPrefix(name, "refs/remotes/") {
			cid, ok := commitArg(r, id)
			if !ok {
				return Reply{}, false
			}
			remotes = append(remotes, cid)
		}
	}
	objs, err := r.objects()
	if err != nil {
		return Reply{}, false
	}
	anc, ok := ancestors(objs, remotes)
	if !ok {
		return Reply{}, false
	}
	if anc[tip] {
		return Reply{}, true
	}
	return Reply{Out: []byte(tip + "\n")}, true
}

// diffCachedQuiet answers `diff --cached --quiet <rev> [--] [paths]`:
// exit 1 when the index differs from rev's tree under the paths.
func diffCachedQuiet(r *repo, inv invocation) (Reply, bool) {
	cached, quiet := false, false
	rev := ""
	var paths []string
	dashdash := false
	for _, a := range inv.args {
		switch {
		case dashdash:
			paths = append(paths, a)
		case a == "--":
			dashdash = true
		case a == "--cached" || a == "--staged":
			cached = true
		case a == "--quiet":
			quiet = true
		case strings.HasPrefix(a, "-"):
			return Reply{}, false
		case rev == "":
			rev = a
		default:
			return Reply{}, false
		}
	}
	if !cached || !quiet || rev == "" {
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
	res := r.resolve(rev + "^{tree}")
	if !res.ok || !res.found {
		return Reply{}, false
	}
	entries, ok := r.readIndex()
	if !ok {
		return Reply{}, false
	}
	objs, err := r.objects()
	if err != nil {
		return Reply{}, false
	}
	tree := map[string]string{} // path -> mode id
	var walk func(id, base string) bool
	walk = func(id, base string) bool {
		o, err := objs.read(id)
		if err != nil || o.typ != typeTree {
			return false
		}
		es, ok := parseTree(o.data)
		if !ok {
			return false
		}
		for _, e := range es {
			p := base + e.name
			if e.typ() == typeTree {
				if ps.matches(p, true) && !walk(e.id, p+"/") {
					return false
				}
				continue
			}
			if e.mode == "160000" {
				return false // submodules: diff.ignoreSubmodules and friends, git
			}
			if ps.matches(p, false) {
				tree[p] = e.mode + " " + e.id
			}
		}
		return true
	}
	if !walk(res.id, "") {
		return Reply{}, false
	}
	n := 0
	for _, e := range entries {
		if !ps.matches(e.path, false) {
			continue
		}
		if e.stage != 0 || e.mode == 0o160000 || e.extended {
			return Reply{}, false
		}
		n++
		if tree[e.path] != strconv.FormatUint(uint64(e.mode), 8)+" "+e.id {
			return Reply{Code: 1}, true
		}
	}
	if n != len(tree) {
		return Reply{Code: 1}, true
	}
	return Reply{}, true
}
