package gitfast

// answer.go — parses an invocation and answers the read shapes lore uses.
// Each handler returns ok=false for any flag or case it does not know, so
// the runner runs git.

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// invocation is one parsed git command line.
type invocation struct {
	dir     string   // where git runs (after -C)
	sub     string   // the subcommand
	args    []string // arguments after the subcommand
	cflags  []string // -c options that may change the answer (kept in keys)
	dropped []string // -c keys left out of cflags (they change no read but a config read of that key)
	literal bool     // --literal-pathspecs
	// hooksDir is what `--git-path hooks` prints, when the Session worked
	// it out from core.hooksPath ("" = ask git).
	hooksDir string
}

// harmlessConfig are -c keys that change no answer of a read handled
// here (lore's store runner passes them on every call).
func harmlessConfig(kv string) bool {
	k, _, _ := strings.Cut(kv, "=")
	k = strings.ToLower(k)
	switch {
	case k == "commit.gpgsign", k == "core.autocrlf", k == "core.quotepath", k == "core.hookspath":
		return true
	case k == "gc.auto", k == "maintenance.auto": // store clones: git's own auto gc off (devco:D-996)
		return true
	case strings.HasPrefix(k, "merge.") && strings.HasSuffix(k, ".driver"):
		return true
	}
	return false
}

// parseInvocation splits the global options from the subcommand.
func parseInvocation(dir string, args []string) (invocation, bool) {
	inv := invocation{dir: dir}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" && i+1 < len(args):
			d := args[i+1]
			if !filepath.IsAbs(d) {
				base := inv.dir
				if base == "" {
					base = "."
				}
				d = filepath.Join(base, d)
			}
			inv.dir = d
			i++
		case a == "-c" && i+1 < len(args):
			kv := args[i+1]
			if harmlessConfig(kv) {
				k, _, _ := strings.Cut(kv, "=")
				inv.dropped = append(inv.dropped, strings.ToLower(k))
			} else {
				inv.cflags = append(inv.cflags, kv)
			}
			i++
		case a == "--literal-pathspecs":
			inv.literal = true
		case strings.HasPrefix(a, "-"):
			return inv, false
		default:
			inv.sub, inv.args = a, args[i+1:]
			if inv.dir == "" {
				inv.dir = "."
			}
			if abs, err := filepath.Abs(inv.dir); err == nil {
				inv.dir = abs
			}
			return inv, true
		}
	}
	return inv, false
}

// readSubs are subcommands that never change config (refs and objects are
// never cached here, so they need no invalidation).
var readSubs = map[string]bool{
	"rev-parse": true, "for-each-ref": true, "check-ref-format": true, "cat-file": true, "ls-tree": true,
	"diff": true, "rev-list": true, "merge-base": true, "log": true, "grep": true, "status": true, "ls-files": true,
	"show": true, "show-ref": true, "check-ignore": true, "var": true, "version": true, "diff-tree": true,
	"diff-index": true, "describe": true, "name-rev": true, "ls-remote": true, "fetch": true, "hash-object": true,
	"update-ref": true, "commit-tree": true, "mktree": true, "read-tree": true, "write-tree": true, "update-index": true,
	"push": true, "add": true, "commit": true, "reset": true, "count-objects": true, "pack-refs": true, "gc": true,
}

// isRead reports whether inv cannot change any config value.
func isRead(inv invocation) bool {
	switch inv.sub {
	case "config":
		return configReads(inv.args)
	case "symbolic-ref":
		return true
	}
	if readSubs[inv.sub] {
		for _, a := range inv.args {
			if a == "--output" || strings.HasPrefix(a, "--output=") {
				return false
			}
		}
		return true
	}
	return false
}

// immutableKey returns the memo key of a read whose answer depends on
// object ids only (cat-file, ls-tree, diff --no-renames, rev-list,
// merge-base over full ids).
func immutableKey(inv invocation, stdin []byte) (string, bool) {
	if len(inv.cflags) != 0 {
		return "", false
	}
	switch inv.sub {
	case "cat-file", "ls-tree", "rev-list", "merge-base", "diff-tree":
	case "diff":
		if !slicesContains(inv.args, "--no-renames") {
			return "", false
		}
	default:
		return "", false
	}
	ids := 0
	paths := false
	for _, a := range inv.args {
		switch {
		case paths:
		case a == "--":
			paths = true
		case strings.HasPrefix(a, "-"):
		case a == "blob" || a == "tree" || a == "commit":
		default:
			base, _, _ := strings.Cut(strings.TrimPrefix(a, "^"), ":")
			if !isFullID(base) {
				return "", false
			}
			ids++
		}
	}
	if len(stdin) > 0 {
		for line := range strings.SplitSeq(strings.TrimRight(string(stdin), "\n"), "\n") {
			base, _, _ := strings.Cut(line, ":")
			if !isFullID(base) {
				return "", false
			}
			ids++
		}
	}
	if ids == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString(inv.dir)
	if inv.literal {
		b.WriteString("\x00--literal-pathspecs")
	}
	b.WriteString("\x00" + inv.sub)
	for _, a := range inv.args {
		b.WriteString("\x00" + a)
	}
	b.WriteString("\x00\x00")
	b.Write(stdin)
	return b.String(), true
}

// slicesContains reports whether list holds s.
func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// answerRead dispatches a read to its handler.
func answerRead(r *repo, inv invocation, stdin []byte) (Reply, bool) {
	if len(inv.cflags) != 0 {
		return Reply{}, false
	}
	switch inv.sub {
	case "rev-parse":
		return revParse(r, inv)
	case "symbolic-ref":
		return symbolicRef(r, inv.args)
	case "for-each-ref":
		return forEachRef(r, inv.args)
	case "check-ref-format":
		return checkRefFormat(inv.args)
	case "cat-file":
		return catFile(r, inv.args, stdin)
	case "ls-tree":
		return lsTree(r, inv)
	case "diff", "diff-tree":
		if slicesContains(inv.args, "--cached") || slicesContains(inv.args, "--staged") {
			return diffCachedQuiet(r, inv)
		}
		return diffTrees(r, inv)
	case "rev-list":
		switch {
		case len(inv.args) > 0 && inv.args[0] == "-1":
			return revListUnpushed(r, inv.args)
		case len(inv.args) > 0 && inv.args[0] == "--count":
			return revList(r, inv.args)
		}
		return revListRange(r, inv.args)
	case "log":
		return logOne(r, inv.args)
	case "merge-base":
		return mergeBase(r, inv.args)
	case "ls-files":
		if slicesContains(inv.args, "--stage") || slicesContains(inv.args, "-s") {
			return lsFilesStage(r, inv)
		}
		return lsFilesPlain(r, inv)
	}
	return Reply{}, false
}

// prefixOf is the path of dir under the work tree top ("" at the top or
// in a bare repository); ok=false when dir is not under the top.
func (r *repo) prefixOf(dir string) (string, bool) {
	if r.top == "" {
		return "", true
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(r.top, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	if strings.HasPrefix(rel, ".git"+string(filepath.Separator)) || rel == ".git" {
		return "", false
	}
	return filepath.ToSlash(rel) + "/", true
}

// gitPathCommon lists the --git-path names git maps to the common dir in a
// linked worktree (path.c common_list), restricted to what lore asks.
func gitPathCommon(p string) bool {
	switch {
	case p == "config", p == "packed-refs", p == "shallow":
		return true
	case strings.HasPrefix(p, "info/"), strings.HasPrefix(p, "objects/"), p == "objects", p == "info":
		return true
	}
	return false
}

// revParse answers the rev-parse forms lore uses.
func revParse(r *repo, inv invocation) (Reply, bool) {
	var out bytes.Buffer
	absolute := false
	verify, quiet := false, false
	var revs []string
	args := inv.args
	endOpts := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if endOpts {
			revs = append(revs, a)
			continue
		}
		switch a {
		case "--path-format=absolute":
			absolute = true
		case "--path-format=relative":
			absolute = false
		case "--verify":
			verify = true
		case "--quiet", "-q":
			quiet = true
		case "--end-of-options":
			endOpts = true
		case "--show-toplevel":
			if r.top == "" {
				return Reply{}, false
			}
			out.WriteString(r.top + "\n")
		case "--show-prefix":
			p, ok := r.prefixOf(inv.dir)
			if !ok {
				return Reply{}, false
			}
			out.WriteString(p + "\n")
		case "--absolute-git-dir":
			out.WriteString(r.gitDir + "\n")
		case "--git-common-dir":
			if !absolute {
				return Reply{}, false
			}
			out.WriteString(r.common + "\n")
		case "--git-dir":
			prefix, ok := r.prefixOf(inv.dir)
			switch {
			case absolute:
				out.WriteString(r.gitDir + "\n")
			case !ok || r.top == "":
				return Reply{}, false
			case prefix == "" && r.rawGitDir == filepath.Join(inv.dir, ".git"):
				out.WriteString(".git\n") // at the top of a plain clone git prints it relative
			default:
				out.WriteString(r.gitDir + "\n")
			}
		case "--is-inside-work-tree":
			if _, ok := r.prefixOf(inv.dir); !ok {
				return Reply{}, false
			}
			out.WriteString(strconv.FormatBool(r.top != "") + "\n")
		case "--is-bare-repository":
			out.WriteString(strconv.FormatBool(r.bare) + "\n")
		case "--show-object-format":
			out.WriteString("sha1\n")
		case "--git-path":
			if i+1 >= len(args) || !absolute {
				return Reply{}, false
			}
			p := args[i+1]
			i++
			if p == "hooks" && inv.hooksDir != "" {
				out.WriteString(inv.hooksDir + "\n")
				continue
			}
			if p == "hooks" || strings.HasPrefix(p, "hooks/") || p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
				return Reply{}, false // core.hooksPath and odd names: git decides
			}
			base := r.gitDir
			if r.gitDir != r.common {
				switch {
				case gitPathCommon(p):
					base = r.common
				case p == "index", p == "index.lock", p == "HEAD":
				default:
					return Reply{}, false
				}
			}
			out.WriteString(filepath.Join(base, filepath.FromSlash(p)) + "\n")
		default:
			if strings.HasPrefix(a, "-") {
				return Reply{}, false
			}
			revs = append(revs, a)
		}
	}
	if verify && len(revs) != 1 {
		return Reply{}, false
	}
	for _, rev := range revs {
		res := r.resolve(rev)
		if !res.ok {
			return Reply{}, false
		}
		if !res.found {
			if verify && quiet && out.Len() == 0 {
				return Reply{Code: 1}, true
			}
			return Reply{}, false // git prints the reason: let it
		}
		out.WriteString(res.id + "\n")
	}
	return Reply{Out: out.Bytes()}, true
}

// shorten is git's shorten_unambiguous_ref (non-strict).
func (r *repo) shorten(name string) (string, bool) {
	rules := append([]string{"%s"}, dwimRules...)
	for i := len(rules) - 1; i > 0; i-- {
		pre, suf, _ := strings.Cut(rules[i], "%s")
		if !strings.HasPrefix(name, pre) || !strings.HasSuffix(name, suf) || len(name) <= len(pre)+len(suf) {
			continue
		}
		short := name[len(pre) : len(name)-len(suf)]
		clash := false
		for j := 0; j < i; j++ {
			full := strings.Replace(rules[j], "%s", short, 1)
			_, _, found, ok := r.readRef(full)
			if !ok {
				return "", false
			}
			if found {
				clash = true
				break
			}
		}
		if !clash {
			return short, true
		}
	}
	return name, true
}

// symbolicRef answers `symbolic-ref [-q|--quiet] [--short] <name>`.
func symbolicRef(r *repo, args []string) (Reply, bool) {
	quiet, short := false, false
	name := ""
	for _, a := range args {
		switch {
		case a == "-q" || a == "--quiet":
			quiet = true
		case a == "--short":
			short = true
		case strings.HasPrefix(a, "-") || name != "":
			return Reply{}, false
		default:
			name = a
		}
	}
	if name == "" || (name != "HEAD" && !strings.HasPrefix(name, "refs/")) {
		return Reply{}, false
	}
	_, target, found, ok := r.readRef(name)
	if !ok {
		return Reply{}, false
	}
	if !found || target == "" {
		if quiet {
			return Reply{Code: 1}, true
		}
		return Reply{}, false
	}
	if short {
		s, ok := r.shorten(target)
		if !ok {
			return Reply{}, false
		}
		target = s
	}
	return Reply{Out: []byte(target + "\n")}, true
}

// forEachRef answers `for-each-ref [--format=F] [--count=N] [--] <prefix>…`
// with the atoms objectname, objecttype and refname.
func forEachRef(r *repo, args []string) (Reply, bool) {
	format := "%(objectname) %(objecttype)\t%(refname)"
	count := -1
	var pats []string
	dashdash := false
	for _, a := range args {
		switch {
		case dashdash:
			pats = append(pats, a)
		case a == "--":
			dashdash = true
		case strings.HasPrefix(a, "--format="):
			format = strings.TrimPrefix(a, "--format=")
		case strings.HasPrefix(a, "--count="):
			n, err := strconv.Atoi(strings.TrimPrefix(a, "--count="))
			if err != nil || n < 0 {
				return Reply{}, false
			}
			count = n
		case strings.HasPrefix(a, "-"):
			return Reply{}, false
		default:
			pats = append(pats, a)
		}
	}
	for _, p := range pats {
		if strings.ContainsAny(p, "*?[\\") {
			return Reply{}, false
		}
	}
	tmpl, ok := parseFormat(format)
	if !ok {
		return Reply{}, false
	}
	refs, ok := r.listRefs()
	if !ok {
		return Reply{}, false
	}
	names := make([]string, 0, len(refs))
	for n := range refs {
		if matchPrefix(n, pats) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var objs *objectStore
	var out bytes.Buffer
	for i, n := range names {
		if count >= 0 && i >= count {
			break
		}
		for _, part := range tmpl {
			switch part {
			case "%(objectname)":
				out.WriteString(refs[n])
			case "%(refname)":
				out.WriteString(n)
			case "%(objecttype)":
				if objs == nil {
					var err error
					if objs, err = r.objects(); err != nil {
						return Reply{}, false
					}
				}
				o, err := objs.read(refs[n])
				if err != nil {
					return Reply{}, false
				}
				out.WriteString(o.typ)
			default:
				out.WriteString(part)
			}
		}
		out.WriteByte('\n')
	}
	return Reply{Out: out.Bytes()}, true
}

// parseFormat splits a for-each-ref format into literal text and atoms.
func parseFormat(f string) ([]string, bool) {
	var parts []string
	var lit strings.Builder
	for i := 0; i < len(f); i++ {
		c := f[i]
		if c != '%' {
			lit.WriteByte(c)
			continue
		}
		if i+1 < len(f) && f[i+1] == '%' {
			lit.WriteByte('%')
			i++
			continue
		}
		if i+1 < len(f) && f[i+1] == '(' {
			end := strings.IndexByte(f[i:], ')')
			if end < 0 {
				return nil, false
			}
			atom := f[i : i+end+1]
			if atom != "%(objectname)" && atom != "%(refname)" && atom != "%(objecttype)" {
				return nil, false
			}
			if lit.Len() > 0 {
				parts = append(parts, lit.String())
				lit.Reset()
			}
			parts = append(parts, atom)
			i += end
			continue
		}
		if i+2 < len(f) {
			if v, err := strconv.ParseUint(f[i+1:i+3], 16, 8); err == nil {
				lit.WriteByte(byte(v))
				i += 2
				continue
			}
		}
		return nil, false
	}
	if lit.Len() > 0 {
		parts = append(parts, lit.String())
	}
	return parts, true
}

// matchPrefix is for-each-ref's pattern rule for literal patterns: the
// whole name, or a prefix ending at a slash.
func matchPrefix(name string, pats []string) bool {
	if len(pats) == 0 {
		return true
	}
	for _, p := range pats {
		switch {
		case name == p:
			return true
		case strings.HasSuffix(p, "/") && strings.HasPrefix(name, p):
			return true
		case strings.HasPrefix(name, p+"/"):
			return true
		}
	}
	return false
}

// checkRefFormat answers `check-ref-format <name>` (git's rules, no
// options): exit 0 valid, 1 invalid, nothing printed.
func checkRefFormat(args []string) (Reply, bool) {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return Reply{}, false
	}
	if validRefName(args[0]) {
		return Reply{}, true
	}
	return Reply{Code: 1}, true
}

// validRefName applies git-check-ref-format's rules 1-10 (no
// --allow-onelevel, no --normalize, no --refspec-pattern).
func validRefName(n string) bool {
	if n == "" || n == "@" || !strings.Contains(n, "/") || strings.HasSuffix(n, "/") || strings.HasPrefix(n, "/") ||
		strings.HasSuffix(n, ".") || strings.Contains(n, "..") || strings.Contains(n, "@{") || strings.Contains(n, "//") {
		return false
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		if c < 0x20 || c == 0x7f || strings.IndexByte(" ~^:?*[\\", c) >= 0 {
			return false
		}
	}
	for comp := range strings.SplitSeq(n, "/") {
		if comp == "" || strings.HasPrefix(comp, ".") || strings.HasSuffix(comp, ".lock") {
			return false
		}
	}
	return true
}

// catFile answers `cat-file blob <rev>`, `cat-file -e <rev>`,
// `cat-file -t <rev>`, `cat-file --batch` and `--batch-check`.
func catFile(r *repo, args []string, stdin []byte) (Reply, bool) {
	objs, err := r.objects()
	if err != nil {
		return Reply{}, false
	}
	switch {
	case len(args) == 1 && (args[0] == "--batch" || args[0] == "--batch-check"):
		var out bytes.Buffer
		if len(stdin) == 0 {
			return Reply{}, true
		}
		for line := range strings.SplitSeq(strings.TrimSuffix(string(stdin), "\n"), "\n") {
			res := r.resolve(line)
			if !res.ok {
				return Reply{}, false
			}
			if !res.found {
				fmt.Fprintf(&out, "%s missing\n", line)
				continue
			}
			o, err := objs.read(res.id)
			if r.absent(err) {
				fmt.Fprintf(&out, "%s missing\n", line)
				continue
			}
			if err != nil {
				return Reply{}, false
			}
			fmt.Fprintf(&out, "%s %s %d\n", res.id, o.typ, len(o.data))
			if args[0] == "--batch" {
				out.Write(o.data)
				out.WriteByte('\n')
			}
		}
		return Reply{Out: out.Bytes()}, true
	case len(args) == 2 && (args[0] == "blob" || args[0] == "-e" || args[0] == "-t"):
		res := r.resolve(args[1])
		if !res.ok || !res.found {
			return Reply{}, false // git's own message for a missing object or path
		}
		o, err := objs.read(res.id)
		if err != nil {
			return Reply{}, false
		}
		switch args[0] {
		case "-e":
			return Reply{}, true
		case "-t":
			return Reply{Out: []byte(o.typ + "\n")}, true
		}
		if o.typ != typeBlob {
			return Reply{}, false
		}
		return Reply{Out: o.data}, true
	}
	return Reply{}, false
}

// asksHooks reports whether a rev-parse asks for --git-path hooks.
func asksHooks(inv invocation) bool {
	for i := 0; i+1 < len(inv.args); i++ {
		if inv.args[i] == "--git-path" && inv.args[i+1] == "hooks" {
			return true
		}
	}
	return false
}
