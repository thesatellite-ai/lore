package gitfast

// config.go — config reads answered from one `git config --list -z
// --show-scope` per folder (and -c options), kept until the next write
// through any runner.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// configSnap is one folder's merged config, in git's order.
type configSnap struct {
	entries   []configEntry
	dir       string // the folder it was read in
	cflagsKey string // the -c options it was read with
}

// configEntry is one key/value with the scope it came from. hasValue is
// false for a bare "key" line (implicit true).
type configEntry struct {
	scope, key, value string
	hasValue          bool
}

// configReadFlags are the read actions this package answers.
var configReadFlags = map[string]bool{"--get": true, "--get-all": true, "--get-regexp": true, "--list": true, "-l": true, "--get-urlmatch": true}

// configReads reports whether config's arguments only read (a lone key,
// `git config <key>`, is the implicit --get).
func configReads(args []string) bool {
	if len(args) == 1 && !strings.HasPrefix(args[0], "-") {
		return true
	}
	read := false
	for _, a := range args {
		switch {
		case configReadFlags[a]:
			read = true
		case a == "--global", a == "--system", a == "--local", a == "--worktree", a == "--null", a == "-z",
			a == "--name-only", a == "--show-origin", a == "--show-scope", a == "--includes", a == "--no-includes",
			a == "--bool", a == "--int", a == "--path", strings.HasPrefix(a, "--type="):
		case strings.HasPrefix(a, "-"):
			return false
		}
	}
	return read
}

// answerConfig answers --get / --get-all / --get-regexp (optionally with
// --show-scope or one scope flag) from the folder's snapshot.
func (s *Session) answerConfig(ctx context.Context, inv invocation) (Reply, bool) {
	action, arg, scope := "", "", ""
	showScope := false
	for _, a := range inv.args {
		switch {
		case a == "--get" || a == "--get-all" || a == "--get-regexp":
			if action != "" {
				return Reply{}, false
			}
			action = a
		case a == "--show-scope":
			showScope = true
		case a == "--global" || a == "--system" || a == "--local" || a == "--worktree":
			if scope != "" {
				return Reply{}, false
			}
			scope = strings.TrimPrefix(a, "--")
		case strings.HasPrefix(a, "-"):
			return Reply{}, false
		case arg == "":
			arg = a
		default:
			return Reply{}, false // a value-pattern argument: git
		}
	}
	if action == "" && arg != "" && len(inv.args) == 1 {
		action = "--get" // `git config <key>`
	}
	if action == "" || arg == "" || (showScope && action != "--get") {
		return Reply{}, false
	}
	snap, ok := s.configSnapshot(ctx, inv)
	if !ok {
		return Reply{}, false
	}
	var out bytes.Buffer
	switch action {
	case "--get", "--get-all":
		key, ok := canonicalKey(arg)
		if !ok || (scope == "" && slicesContains(inv.dropped, strings.ToLower(key))) {
			return Reply{}, false // a -c value would answer it (one scope never sees -c)
		}
		var hits []configEntry
		for _, e := range snap.entries {
			if e.key == key && (scope == "" || e.scope == scope) {
				hits = append(hits, e)
			}
		}
		if len(hits) == 0 {
			return Reply{Code: 1}, true
		}
		if action == "--get" {
			hits = hits[len(hits)-1:]
		}
		for _, e := range hits {
			if !e.hasValue {
				return Reply{}, false
			}
			if showScope {
				out.WriteString(e.scope + "\t")
			}
			out.WriteString(e.value + "\n")
		}
	case "--get-regexp":
		re, err := regexp.Compile(arg)
		if err != nil || escapedLetter(arg) {
			return Reply{}, false // POSIX ERE and RE2 differ on \\d, \\w …: git
		}
		for _, d := range inv.dropped {
			if scope == "" && re.MatchString(d) {
				return Reply{}, false
			}
		}
		for _, e := range snap.entries {
			if (scope == "" || e.scope == scope) && re.MatchString(e.key) {
				out.WriteString(e.key)
				if e.hasValue {
					out.WriteString(" " + e.value)
				}
				out.WriteByte('\n')
			}
		}
		if out.Len() == 0 {
			return Reply{Code: 1}, true
		}
	}
	return Reply{Out: out.Bytes()}, true
}

// canonicalKey lowercases a key's section and name, keeping the
// subsection; ok=false for a key git would reject.
func canonicalKey(k string) (string, bool) {
	first := strings.IndexByte(k, '.')
	last := strings.LastIndexByte(k, '.')
	if first <= 0 || last == len(k)-1 {
		return "", false
	}
	for _, c := range k[last+1:] {
		if !(c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return "", false
		}
	}
	if first == last {
		return strings.ToLower(k), true
	}
	return strings.ToLower(k[:first]) + k[first:last] + "." + strings.ToLower(k[last+1:]), true
}

// configSnapshot runs (once per folder, -c options and generation) `git
// config --list -z --show-scope`.
func (s *Session) configSnapshot(ctx context.Context, inv invocation) (*configSnap, bool) {
	cflagsKey := strings.Join(inv.cflags, "\x00")
	key := inv.dir + "\x00" + cflagsKey
	s.mu.Lock()
	if snap, ok := s.configs[key]; ok {
		s.mu.Unlock()
		return snap, true
	}
	gen, writing := s.gen, s.writing
	s.mu.Unlock()
	if snap, ok := s.composeSnapshot(inv, cflagsKey); ok {
		s.mu.Lock()
		if writing == 0 && s.writing == 0 && s.gen == gen {
			s.configs[key] = snap
		}
		s.mu.Unlock()
		return snap, true
	}
	r := s.repoFor(inv.dir)
	persistAt := ""
	if r != nil {
		persistAt = s.configPersistPath(inv.dir, cflagsKey)
		if snap, ok := loadPersistedConfig(persistAt); ok && persistAt != "" {
			snap.dir, snap.cflagsKey = inv.dir, cflagsKey
			s.mu.Lock()
			if writing == 0 && s.writing == 0 && s.gen == gen {
				s.configs[key] = snap
			}
			s.mu.Unlock()
			return snap, true
		}
	}
	var args []string
	for _, c := range inv.cflags {
		args = append(args, "-c", c)
	}
	args = append(args, "config", "--list", "-z", "--show-scope", "--show-origin")
	raw, err := s.execGit(ctx, inv.dir, args)
	if err != nil {
		return nil, false
	}
	snap := &configSnap{dir: inv.dir, cflagsKey: cflagsKey}
	var origins []string
	fields := strings.Split(string(raw), "\x00")
	for i := 0; i+2 < len(fields); i += 3 {
		scope, origin, kv := fields[i], fields[i+1], fields[i+2]
		k, v, has := strings.Cut(kv, "\n")
		snap.entries = append(snap.entries, configEntry{scope: scope, key: k, value: v, hasValue: has})
		if f, ok := strings.CutPrefix(origin, "file:"); ok {
			if !filepath.IsAbs(f) {
				f = filepath.Join(inv.dir, f)
			}
			origins = append(origins, filepath.Clean(f))
		} else if origin != "command line:" {
			persistAt = "" // a blob or stdin origin: not fingerprinted
		}
	}
	if persistAt != "" && writing == 0 {
		savePersistedConfig(persistAt, r, snap, origins)
	}
	s.mu.Lock()
	if writing == 0 && s.writing == 0 && s.gen == gen {
		s.configs[key] = snap
	}
	s.mu.Unlock()
	return snap, true
}

// escapedLetter reports a backslash before a letter or digit in a regexp.
func escapedLetter(re string) bool {
	for i := 0; i+1 < len(re); i++ {
		if re[i] == '\\' {
			c := re[i+1]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
				return true
			}
			i++
		}
	}
	return false
}

// composeSnapshot builds dir's snapshot without a git process from one
// already read for another folder (same -c options): its system, global
// and command entries, when every includeIf condition among them comes
// out the same for both folders (gitdir: patterns this package can
// evaluate; any other condition means "ask git"), plus dir's own
// repository config parsed here (plainConfig already refused includes).
func (s *Session) composeSnapshot(inv invocation, cflagsKey string) (*configSnap, bool) {
	r := s.repoFor(inv.dir)
	if r == nil {
		return nil, false
	}
	s.mu.Lock()
	var base *configSnap
	for _, snap := range s.configs {
		if snap.cflagsKey == cflagsKey && snap.dir != "" {
			base = snap
			break
		}
	}
	s.mu.Unlock()
	if base == nil {
		return nil, false
	}
	var baseRepo *repo
	var shared []configEntry
	for _, e := range base.entries {
		switch e.scope {
		case "local", "worktree":
			continue
		case "system", "global", "command":
		default:
			return nil, false
		}
		if strings.HasPrefix(e.key, "includeif.") {
			cond, ok := strings.CutSuffix(strings.TrimPrefix(e.key, "includeif."), ".path")
			if !ok {
				return nil, false
			}
			if baseRepo == nil {
				if baseRepo = s.repoFor(base.dir); baseRepo == nil {
					return nil, false
				}
			}
			a, okA := gitdirCondition(cond, baseRepo)
			b, okB := gitdirCondition(cond, r)
			if !okA || !okB || a != b {
				return nil, false
			}
		}
		shared = append(shared, e)
	}
	local, ok := parseLocalConfig(filepath.Join(r.common, "config"))
	if !ok {
		return nil, false
	}
	snap := &configSnap{dir: inv.dir, cflagsKey: cflagsKey}
	// git's order: system, global, local, worktree, command
	for _, e := range shared {
		if e.scope != "command" {
			snap.entries = append(snap.entries, e)
		}
	}
	snap.entries = append(snap.entries, local...)
	for _, e := range shared {
		if e.scope == "command" {
			snap.entries = append(snap.entries, e)
		}
	}
	return snap, true
}

// gitdirCondition evaluates an includeIf "gitdir:" / "gitdir/i:"
// condition for r. ok=false: a condition this package does not evaluate.
func gitdirCondition(cond string, r *repo) (bool, bool) {
	fold := false
	pat, ok := strings.CutPrefix(cond, "gitdir:")
	if !ok {
		if pat, ok = strings.CutPrefix(cond, "gitdir/i:"); !ok {
			return false, false
		}
		fold = true
	}
	if strings.HasPrefix(pat, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return false, false
		}
		pat = filepath.ToSlash(home) + pat[1:]
	} else if strings.HasPrefix(pat, "./") || strings.HasPrefix(pat, "../") || pat == "" {
		return false, false // relative to the including file: git
	}
	lead := !strings.HasPrefix(pat, "/")
	pat = strings.TrimPrefix(pat, "**/")
	trail := strings.HasSuffix(pat, "/")
	pat = strings.TrimSuffix(pat, "/")
	if strings.HasSuffix(pat, "/**") {
		trail, pat = true, strings.TrimSuffix(pat, "/**")
	}
	if strings.ContainsAny(pat, "*?[\\") || pat == "" {
		return false, false
	}
	match := func(p string) bool {
		p = filepath.ToSlash(p)
		q := pat
		if fold {
			p, q = strings.ToLower(p), strings.ToLower(q)
		}
		switch {
		case !lead && trail:
			return strings.HasPrefix(p+"/", q+"/") && p != q
		case !lead:
			return p == q
		case trail:
			return strings.Contains("/"+p+"/", "/"+strings.TrimPrefix(q, "/")+"/") && !strings.HasSuffix("/"+p, "/"+strings.TrimPrefix(q, "/"))
		default:
			return p == q || strings.HasSuffix(p, "/"+strings.TrimPrefix(q, "/"))
		}
	}
	return match(r.gitDir) || (r.rawGitDir != "" && match(r.rawGitDir)), true
}

// parseLocalConfig reads a repository config file the way git does for
// the plain syntax: sections, subsections, keys with or without values,
// quotes, comments and the escapes \" \\ \n \t \b. Anything else (a
// continuation line, another escape, a malformed line) is ok=false.
func parseLocalConfig(path string) ([]configEntry, bool) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, true
	}
	if err != nil {
		return nil, false
	}
	var out []configEntry
	section := ""
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		t := strings.TrimLeft(line, " \t")
		if t == "" || t[0] == '#' || t[0] == ';' {
			continue
		}
		if t[0] == '[' {
			name, rest, ok := parseSectionHeader(t)
			if !ok {
				return nil, false
			}
			section = name
			t = strings.TrimLeft(rest, " \t")
			if t == "" || t[0] == '#' || t[0] == ';' {
				continue
			}
		}
		if section == "" {
			return nil, false
		}
		i := 0
		for i < len(t) && (t[i] == '-' || t[i] >= '0' && t[i] <= '9' || t[i] >= 'a' && t[i] <= 'z' || t[i] >= 'A' && t[i] <= 'Z') {
			i++
		}
		if i == 0 || !(t[0] >= 'a' && t[0] <= 'z' || t[0] >= 'A' && t[0] <= 'Z') {
			return nil, false
		}
		key := section + "." + strings.ToLower(t[:i])
		rest := strings.TrimLeft(t[i:], " \t")
		if rest == "" || rest[0] == '#' || rest[0] == ';' {
			out = append(out, configEntry{scope: "local", key: key})
			continue
		}
		if rest[0] != '=' {
			return nil, false
		}
		val, ok := parseConfigValue(rest[1:])
		if !ok {
			return nil, false
		}
		out = append(out, configEntry{scope: "local", key: key, value: val, hasValue: true})
	}
	return out, true
}

// parseSectionHeader parses `[name]`, `[name "sub"]` or `[name.sub]` and
// returns the canonical "name" / "name.sub" and what follows the bracket.
func parseSectionHeader(t string) (string, string, bool) {
	end := strings.IndexByte(t, ']')
	if end < 0 {
		return "", "", false
	}
	inner := t[1:end]
	if sp := strings.IndexAny(inner, " \t"); sp >= 0 {
		name := inner[:sp]
		sub := strings.TrimLeft(inner[sp:], " \t")
		if len(sub) < 2 || sub[0] != '"' || sub[len(sub)-1] != '"' || !validSectionName(name) {
			return "", "", false
		}
		sub = sub[1 : len(sub)-1]
		if strings.ContainsAny(sub, "\\\"") {
			return "", "", false // escaped subsection: git
		}
		// the bracket found may be inside the quotes: be strict
		if strings.Contains(sub, "]") {
			return "", "", false
		}
		return strings.ToLower(name) + "." + sub, t[end+1:], true
	}
	if !validSectionName(inner) {
		return "", "", false
	}
	return strings.ToLower(inner), t[end+1:], true
}

// validSectionName is git's section name rule (alnum, '-', '.').
func validSectionName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c == '-' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// parseConfigValue parses the value after '=' as git's parse_value does:
// blanks inside the value are kept as written, leading and trailing
// blanks go, # and ; start a comment outside quotes.
func parseConfigValue(v string) (string, bool) {
	var b strings.Builder
	quoted := false
	var space []byte
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !quoted {
			if c == ' ' || c == '\t' {
				if b.Len() > 0 {
					space = append(space, c)
				}
				continue
			}
			if c == ';' || c == '#' {
				break
			}
		}
		if bytes.IndexByte(space, '\t') >= 0 {
			return "", false // older gits turn an inner tab into a space: git decides
		}
		b.Write(space)
		space = space[:0]
		switch c {
		case '"':
			quoted = !quoted
		case '\\':
			if i+1 >= len(v) {
				return "", false // continuation line: git
			}
			i++
			switch v[i] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", false // \b and unknown escapes: git
			}
		default:
			b.WriteByte(c)
		}
	}
	if quoted {
		return "", false
	}
	return b.String(), true
}
