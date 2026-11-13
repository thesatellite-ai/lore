package wire

// hooksecrets.go — the secret half of lore's commit and push checks on
// cmd/lore (SECRETS_AND_REFERENCES 19.4, D3, D4; ported from saas/cmd/cli
// secret_scanner.go coreSecrets, P3-MODEB-10). Every repo, not only a
// client repo: a credential must never reach any remote.
//
//	docs (path → bytes: staged files, a commit message, pushed blobs)
//	  ─▶ HookScanner (team rules + repo .gitleaks.toml; a personal "off"
//	     never applies to a shared commit)
//	  ─▶ a .lore/data row file is scanned field by field with the
//	     committed secret_allows entries (only entries in the index count),
//	     then its whole text for anything no field held; any other file as
//	     plain text
//	  ─▶ a commit: findings the same row already held at HEAD are not new
//	     (hooksecrets_history.go), one notice line counts them
//	  ─▶ E_LEAK_BLOCKED naming file, line and rule, never the value
//
// Fails closed: a scanner or allow list that cannot be built refuses.

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
)

// secret_allows row fields and folder (schema.Builtin).
const (
	allowsDirRel    = ".lore/data/secret_allows/"
	allowFieldRowID = "row_id"
	allowFieldField = "field"
	allowFieldRule  = "rule_id"
	allowFieldHash  = "match_hash"
	allowFieldPath  = "path"
	maxMatchHash    = 1<<32 - 1
	loreDataMark    = ".lore/data/"
)

// HookSecrets scans docs for credentials before a commit or push from the
// repository at root; where names the action ("commit", "push").
// A commit's findings already in history (hooksecrets_history.go) go to
// the notice sink as one line.
func (a *App) HookSecrets(ctx context.Context, root, where string, docs map[string][]byte) error {
	old, err := a.hookSecrets(ctx, root, where, docs)
	if old > 0 {
		a.notice(historyLine(old))
	}
	return err
}

// hookSecrets is HookSecrets returning, instead of printing, how many
// findings of a commit were let through as already in history: `lore check
// leaks` runs on its own App, whose notice sink the command hooks never
// set, so it prints that line as its result.
func (a *App) hookSecrets(ctx context.Context, root, where string, docs map[string][]byte) (inHistory int, err error) {
	if len(docs) == 0 {
		return 0, nil
	}
	sc, err := a.HookScanner(ctx)
	if err != nil {
		return 0, errs.Wrap(errs.LeakBlocked, "cannot check for secrets: the scanner could not be built: "+err.Error(), err)
	}
	allows, err := committedAllows(ctx, root)
	if err != nil {
		return 0, err
	}
	_, isGit, inStore := rowfs.StoreTop(root)
	inStore = inStore && isGit
	paths := make([]string, 0, len(docs))
	for p := range docs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	hits := scanDocs(sc, allows, paths, docs, inStore)
	if where == checkWhereCommit {
		// D2: a commit is blocked only for what it adds; a value the same
		// row already held at HEAD (a format rewrite, a moved row file) is
		// counted and named once (hooksecrets_history.go).
		hits, inHistory = dropHistoryHits(ctx, sc, root, inStore, paths, docs, hits)
	}
	var lines, refused []string
	named := true // every hit got its own ready allow line
	for i, p := range paths {
		for _, h := range hits[i] {
			at := fmt.Sprintf("%s:%d", p, h.Line)
			if h.Field != "" {
				at += " (field " + h.Field + ")"
			}
			lines = append(lines, fmt.Sprintf("  %-40s secret: rule %s match %s", at, h.Rule, h.Preview))
			l := ""
			if where == checkWhereCommit {
				l = allowLineOf(p, h, inStore)
			}
			if l != "" {
				lines = append(lines, "      not a secret? "+l)
			} else {
				named = false
			}
			refused = append(refused, "path="+p+" rule="+h.Rule)
		}
	}
	if len(lines) == 0 {
		return inHistory, nil
	}
	a.auditHookRefused(ctx, where, refused)
	return inHistory, errs.New(errs.LeakBlocked, "blocked, this "+where+" carries a secret:\n"+strings.Join(lines, "\n")).
		WithHint(leakHint(inStore, named))
}

// allowLineOf is the exact `lore secrets allow` command that accepts the
// one finding h of the file at p, with its path (or row id and field),
// rule and match hash filled in; only the reason is left to write. ""
// where no entry can accept it: a commit message, or a store file that is
// not a row. A refusal that names the command keeps people from reaching
// for --no-verify (user report).
func allowLineOf(p string, h leakcheck.SecretHit, inStore bool) string {
	const reason = ` --reason "<why>"`
	switch {
	case p == checkWhereCommitMsg:
		return ""
	case isRowDocPath(p, inStore):
		id, err := ids.ParseAny(strings.TrimSuffix(path.Base(p), ".json"))
		if err != nil || h.Field == "" {
			return ""
		}
		return "lore secrets allow " + id.String() + " --rule " + h.Rule + " --field " + shellWord(h.Field) + " --match-hash " + h.Preview + reason
	case inStore:
		return ""
	}
	return "lore secrets allow --file " + shellWord(p) + " --rule " + h.Rule + " --match-hash " + h.Preview + reason
}

// leakHint is the next step of a refused commit or push. A code repo keeps
// its allow entries in .lore/data/secret_allows, to be committed with the
// change; a store clone has no .lore/data: `lore secrets allow` there
// writes and commits the entry itself, as <folder>/secret_allows/<id>.json
// in the store, so the person only commits again (never --no-verify).
// named: every finding was printed with its own ready allow line, so the
// hint points at those lines instead of a template to fill in.
func leakHint(inStore, named bool) string {
	vault := "store the value in your vault and reference it (${secret:…}); if it is not a secret: lore secrets allow <row id> --rule <rule> --field <field> --match-hash <the sha256:… match shown> --reason \"<why>\""
	if named {
		vault = "store the value in your vault and reference it (${secret:…}, or a placeholder such as ${DB_PASSWORD}); if a finding is not a secret, run the `not a secret?` line printed under it with your reason (a .gitleaksignore <path>:<rule>:<line> entry works too)"
	}
	if inStore {
		return vault + " (a store has no .lore/data: lore writes and commits the entry itself, as <folder>/secret_allows/<id>.json), then commit again (no --no-verify needed)"
	}
	return vault + ", add the new .lore/data/secret_allows file, then commit again (no --no-verify needed)"
}

// hookIndexEnv passes on the index git gave the hook. `git commit -a`,
// `git commit <paths>` and some `--amend` forms run the hooks with
// GIT_INDEX_FILE naming a temporary index that holds what is being
// committed; gitEnv drops inherited repo-locating variables, so without
// this every check read the default .git/index and a secret added by
// `git commit -a` was never seen (docs bug B1). A relative value is
// relative to the directory git ran the hook in, the worktree top.
func hookIndexEnv(top string) []string {
	idx := os.Getenv(envGitIndexFile)
	if idx == "" {
		return nil
	}
	if !filepath.IsAbs(idx) {
		if top != "" {
			idx = filepath.Join(top, idx)
		} else if abs, err := filepath.Abs(idx); err == nil {
			idx = abs
		}
	}
	return []string{"GIT_INDEX_FILE=" + idx}
}

// storeAllowsPathspec matches every secret_allows row file of a store
// clone: <folder>/secret_allows/<id>.json at any folder depth (and the old
// <folder>/.lore/data/secret_allows/ layout, D-w).
const storeAllowsPathspec = ":(glob)**/" + schema.TableSecretAllows + "/*.json"

// committedAllows reads every secret_allows row file in the git index under
// root's .lore/data (19.4: only the tree being committed counts), and the
// repo top's .gitleaksignore from the same index (gitleaksignore.go). Not
// a git checkout: none. A file that does not decode allows nothing.
func committedAllows(ctx context.Context, root string) (hookAllows, error) {
	top := gitTop(ctx, root)
	if top == "" {
		return hookAllows{}, nil
	}
	env := hookIndexEnv(top)
	list := gitRun{dir: root, env: env, args: []string{"ls-files", "-z", "--full-name", "--", allowsDirRel}}
	if _, isGit, inStore := rowfs.StoreTop(root); inStore && isGit {
		list = gitRun{dir: top, env: env, args: []string{"ls-files", "-z", "--full-name", "--", storeAllowsPathspec}}
	}
	out, err := list.output(ctx)
	if err != nil {
		return hookAllows{}, errs.Wrap(errs.Internal, "list committed secret allow entries", err)
	}
	paths := strings.Split(string(out), "\x00")
	blobs, err := indexBlobs(ctx, top, env, append(paths, gitleaksIgnoreFile)) // one git process, not one per entry
	if err != nil {
		return hookAllows{}, err
	}
	var allows hookAllows
	if b, ok := blobs[gitleaksIgnoreFile]; ok {
		allows.ignore = parseGitleaksIgnore(b)
	}
	for _, p := range paths {
		b, ok := blobs[p]
		if !ok {
			continue
		}
		if al, ok := allowOfFile(b); ok {
			allows.entries = append(allows.entries, al)
		}
	}
	return allows, nil
}

// allowOfFile reads one secret_allows row file; false for a deleted,
// malformed or incomplete entry (every part must be there: fail closed).
func allowOfFile(b []byte) (scan.Allow, bool) {
	r, err := row.Decode(b)
	if err != nil || r.Deleted {
		return scan.Allow{}, false
	}
	n, ok := r.Fields[allowFieldHash].(value.Number)
	if !ok {
		return scan.Allow{}, false
	}
	h, err := n.Int64()
	if err != nil || h < 0 || h > maxMatchHash {
		return scan.Allow{}, false
	}
	if path := fieldText(r, allowFieldPath); path != "" {
		// a file entry (`secrets allow --file`): no row, the path is the key
		return scan.Allow{Field: fileAllowKey(path), RuleID: fieldText(r, allowFieldRule), MatchHash: uint32(h)}, true
	}
	target, err := ids.ParseAny(fieldText(r, allowFieldRowID))
	if err != nil {
		return scan.Allow{}, false
	}
	return scan.Allow{RowID: target, Field: fieldText(r, allowFieldField), RuleID: fieldText(r, allowFieldRule), MatchHash: uint32(h)}, true
}

// fileAllowPrefix marks a file entry among the allows: its Allow has no
// RowID and Field "file:<repo-relative path>", so scan.Filter (row ids)
// never matches it and fileAllowed matches only that path.
const fileAllowPrefix = "file:"

// fileAllowKey is the Allow.Field of a file entry for path.
func fileAllowKey(path string) string { return fileAllowPrefix + path }

// fileAllowed drops the findings of a plain file at p that a committed
// file entry accepts (same path, rule and match hash).
func fileAllowed(p string, found []scan.Finding, allows []scan.Allow) []scan.Finding {
	key := fileAllowKey(p)
	out := found[:0:0]
	for _, f := range found {
		ok := false
		for _, a := range allows {
			if a.RowID.IsZero() && a.Field == key && a.RuleID == f.RuleID && a.MatchHash == f.MatchHash {
				ok = true
				break
			}
		}
		if !ok {
			out = append(out, f)
		}
	}
	return out
}

// scanDocs scans every document on GOMAXPROCS goroutines (the scanner is
// safe for concurrent use; item 30: a 5,000-row commit scanned one file
// after another). hits[i] are the findings of paths[i], so the refusal
// lists them in path order whatever finished first.
func scanDocs(sc scan.Scanner, allows hookAllows, paths []string, docs map[string][]byte, inStore bool) [][]leakcheck.SecretHit {
	hits := make([][]leakcheck.SecretHit, len(paths))
	if len(paths) == 0 {
		return hits
	}
	_ = sc.Scan("", "") // build a lazy scanner once, before the workers share it
	workers := min(runtime.GOMAXPROCS(0), len(paths))
	next := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				hits[i] = scanDoc(sc, allows, paths[i], docs[paths[i]], inStore)
			}
		}()
	}
	for i := range paths {
		next <- i
	}
	close(next)
	wg.Wait()
	return hits
}

// scanDoc scans one document (see the file comment).
// inStore: p is a path in a store clone, whose row files sit at
// <folder>/<table>/<id>.json rather than under .lore/data.
// A hit a .gitleaksignore fingerprint names (path, rule, line) is dropped
// last, on the hits as the refusal would print them.
func scanDoc(sc scan.Scanner, allows hookAllows, p string, b []byte, inStore bool) []leakcheck.SecretHit {
	content := string(b)
	if strings.Contains("/"+p, "/"+loreDataMark) || (inStore && isStoreRowPath(p)) {
		if r, err := row.Decode(b); err == nil && !r.ID.IsZero() {
			return allows.ignore.drop(p, oneHitPerValue(scanRowDoc(sc, allows.entries, r, content)))
		}
	}
	// The path is the field of a plain document, so path-scoped filters
	// (a go.sum module hash) can tell the file; hits keep no field.
	found := sc.Scan(p, content)
	return allows.ignore.drop(p, oneHitPerValue(hitsOf(content, "", sameValueAllowed(found, fileAllowed(p, found, allows.entries)))))
}

// sameValueAllowed drops from kept (found after the allow entries) every
// finding whose value an entry allowed under another rule: one value is
// often matched by a lore rule and the gitleaks rule it covers, the
// refusal names one of them (oneHitPerValue), and an entry written for
// that one must accept the value, not leave the other rule blocking it.
func sameValueAllowed(found, kept []scan.Finding) []scan.Finding {
	if len(kept) == len(found) {
		return kept
	}
	left := map[scan.Finding]bool{}
	for _, f := range kept {
		left[f] = true
	}
	allowed := map[string]bool{} // field + fingerprint of each allowed value
	for _, f := range found {
		if !left[f] {
			allowed[f.Field+"\x00"+f.Preview] = true
		}
	}
	out := kept[:0:0]
	for _, f := range kept {
		if !allowed[f.Field+"\x00"+f.Preview] {
			out = append(out, f)
		}
	}
	return out
}

// oneHitPerValue keeps one hit per (line, field, fingerprint) (W-9, spec
// 19.10): lore's own rule and the gitleaks rule it covers (github_token
// and github-pat) both match one token, and the refusal listed the value
// twice under two rules. The kept rule is chosen the same way every run:
// a lore pack rule (gitleaks.Covers) before any other, then the smaller
// id. Order otherwise stays as found.
func oneHitPerValue(hits []leakcheck.SecretHit) []leakcheck.SecretHit {
	if len(hits) < 2 {
		return hits
	}
	type key struct {
		line         int
		field, print string
	}
	own := gitleaks.Covers()
	better := func(a, b string) bool { // a is preferred over b
		_, ownA := own[a]
		_, ownB := own[b]
		if ownA != ownB {
			return ownA
		}
		return a < b
	}
	at := map[key]int{}
	out := make([]leakcheck.SecretHit, 0, len(hits))
	for _, h := range hits {
		k := key{h.Line, h.Field, h.Preview}
		if i, dup := at[k]; dup {
			if better(h.Rule, out[i].Rule) {
				out[i] = h
			}
			continue
		}
		at[k] = len(out)
		out = append(out, h)
	}
	return out
}

// isStoreRowPath reports whether p is a row file of a store clone.
func isStoreRowPath(p string) bool {
	_, _, ok := rowfs.LayoutStore.SplitRowPath(p)
	return ok
}

// secretKey identifies one secret value under one rule.
type secretKey struct {
	rule string
	hash uint32
}

// scanRowDoc scans a row file field by field with its allow entries, then
// the whole text for anything no field held.
func scanRowDoc(sc scan.Scanner, allows []scan.Allow, r row.Row, content string) []leakcheck.SecretHit {
	names := make([]string, 0, len(r.Fields))
	for n := range r.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	seen := map[secretKey]bool{}
	var out []leakcheck.SecretHit
	for _, name := range names {
		for _, text := range fieldTexts(r.Fields[name]) {
			found := sc.Scan(name, text)
			for _, f := range found {
				seen[secretKey{f.RuleID, f.MatchHash}] = true
			}
			line := lineOfField(content, name)
			for _, f := range sameValueAllowed(found, scan.Filter(r.ID, found, allows)) {
				out = append(out, leakcheck.SecretHit{Line: line, Rule: f.RuleID, Preview: f.Preview, Field: name, Hash: f.MatchHash})
			}
		}
	}
	var rest []scan.Finding
	for _, f := range sc.Scan("", content) {
		if !seen[secretKey{f.RuleID, f.MatchHash}] {
			rest = append(rest, f)
		}
	}
	return append(out, hitsOf(content, "", rest)...)
}

// fieldTexts are the texts of a field the write path scans: a text, each
// item of a list, the JSON form of anything else.
func fieldTexts(v value.Value) []string {
	if v == nil {
		return nil
	}
	switch v.Kind() {
	case value.KindText:
		return []string{v.String()}
	case value.KindList:
		return value.ItemStrings(v)
	default: // every other kind is scanned in its JSON form, else its text
		if b, ok := value.JSONForm(v); ok {
			return []string{string(b)}
		}
		return []string{v.String()}
	}
}

// lineOfField is the 1-based line of a field's key in a row file.
func lineOfField(content, name string) int {
	i := strings.Index(content, `"`+name+`"`)
	if i < 0 {
		return 1
	}
	return strings.Count(content[:i], "\n") + 1
}

// hitsOf maps scanner findings over content to hits.
func hitsOf(content, field string, fs []scan.Finding) []leakcheck.SecretHit {
	out := make([]leakcheck.SecretHit, 0, len(fs))
	for _, f := range fs {
		line := f.Line
		if f.Offset >= 0 && f.Offset <= len(content) {
			line = strings.Count(content[:f.Offset], "\n") + 1
		}
		out = append(out, leakcheck.SecretHit{Line: line, Rule: f.RuleID, Preview: f.Preview, Field: field})
	}
	return out
}

// Hook names of secret.hook_refused (v0.1.17 secrets_hookaudit.go).
const (
	hookPreCommit = "pre-commit"
	hookPrePush   = "pre-push"
)

// auditHookRefused records secret.hook_refused for every finding a commit
// or push check refused (SECRETS 16, FA-AGENT-7): hook, path and rule id,
// never the text or its preview. Other checks record nothing. Best effort
// by design: the check already refuses, so a failed audit append must not
// turn the refusal into another error; it goes to the notice sink.
func (a *App) auditHookRefused(ctx context.Context, where string, refused []string) {
	hook := ""
	switch where {
	case checkWhereCommit:
		hook = hookPreCommit
	case LeakWherePush:
		hook = hookPrePush
	default:
		return
	}
	for _, r := range refused {
		if err := a.AuditSecret(ctx, engine.ActionLeakHookRefused, engine.SecretAudit{Command: hook, Result: "hook=" + hook + " " + r}); err != nil {
			a.notice("lore: warning: secret.hook_refused was not recorded: " + err.Error())
		}
	}
}

var _ opssettings.FindingHasher = (*App)(nil)

// FindingMatchHash implements opssettings.FindingHasher (bug B1-15): the
// commit refusal names a finding by its fingerprint (scan.Mask,
// "sha256:" + hex), the allow entry stores the finding's 32-bit match
// hash. The field's texts are scanned with the commit check's rules
// (HookScanner) and the one finding of rule whose fingerprint starts with
// fingerprint (lower-case hex, no prefix) gives the hash; none, or two
// different values, is refused with the command that lists every finding.
func (a *App) FindingMatchHash(ctx context.Context, r row.Row, field, rule, fingerprint string) (uint32, error) {
	sc, err := a.HookScanner(ctx)
	if err != nil {
		return 0, err
	}
	found := map[uint32]bool{}
	for _, text := range fieldTexts(r.Fields[field]) {
		for _, f := range sc.Scan(field, text) {
			fp := strings.TrimPrefix(f.Preview, "sha256:")
			if f.RuleID == rule && (strings.HasPrefix(fp, fingerprint) || strings.HasPrefix(fingerprint, fp)) {
				found[f.MatchHash] = true
			}
		}
	}
	const list = "list the findings with their exact allow commands: lore check leaks --rows"
	switch len(found) {
	case 1:
		for h := range found {
			return h, nil
		}
	case 0:
		return 0, errs.New(errs.NotFound, "secrets allow: no "+rule+" finding with fingerprint sha256:"+fingerprint+" in the "+field+" of "+r.ID.String()).WithHint(list)
	}
	return 0, errs.New(errs.InvalidInput, "secrets allow: fingerprint sha256:"+fingerprint+" names more than one "+rule+" finding in the "+field+" of "+r.ID.String()).WithHint("give more of the fingerprint, or " + list)
}

var _ opssettings.FileAllower = (*App)(nil)

// RepoFilePath implements opssettings.FileAllower: p (absolute, or
// relative to the working directory when that is inside the repo, else to
// the repo top) as a slash path relative to the repo top, the key the
// commit check compares with. A path outside the repo is refused.
func (a *App) RepoFilePath(ctx context.Context, p string) (string, error) {
	top := gitTop(ctx, a.opts.RepoDir)
	if top == "" {
		return "", errs.New(errs.InvalidInput, "secrets allow --file: "+a.opts.RepoDir+" is not in a git repo")
	}
	top = realPath(top)
	abs := p
	if !filepath.IsAbs(abs) {
		base := top
		if wd, err := os.Getwd(); err == nil && within(top, realPath(wd)) {
			base = realPath(wd)
		}
		abs = filepath.Join(base, p)
	}
	abs = filepath.Join(realPath(filepath.Dir(abs)), filepath.Base(abs))
	rel, err := filepath.Rel(top, abs)
	if err != nil || !within(top, abs) || rel == "." {
		return "", errs.New(errs.InvalidInput, "secrets allow --file: "+p+" is not a file in this repo ("+top+")")
	}
	return filepath.ToSlash(rel), nil
}

// FileFindingHash implements opssettings.FileAllower: the match hash of
// the one finding of rule in the repo file path (as staged, else as on
// disk) whose fingerprint starts with fingerprint.
func (a *App) FileFindingHash(ctx context.Context, path, rule, fingerprint string) (uint32, error) {
	top := gitTop(ctx, a.opts.RepoDir)
	var content []byte
	if top != "" {
		if blobs, err := indexBlobs(ctx, top, hookIndexEnv(top), []string{path}); err == nil {
			content = blobs[path]
		}
	}
	if content == nil {
		b, err := os.ReadFile(filepath.Join(realPath(top), filepath.FromSlash(path)))
		if err != nil {
			return 0, errs.Wrap(errs.NotFound, "secrets allow --file: cannot read "+path, err)
		}
		content = b
	}
	sc, err := a.HookScanner(ctx)
	if err != nil {
		return 0, err
	}
	found := map[uint32]bool{}
	for _, f := range sc.Scan(path, string(content)) {
		fp := strings.TrimPrefix(f.Preview, "sha256:")
		if f.RuleID == rule && (strings.HasPrefix(fp, fingerprint) || strings.HasPrefix(fingerprint, fp)) {
			found[f.MatchHash] = true
		}
	}
	switch len(found) {
	case 1:
		for h := range found {
			return h, nil
		}
	case 0:
		return 0, errs.New(errs.NotFound, "secrets allow --file: no "+rule+" finding with fingerprint sha256:"+fingerprint+" in "+path).
			WithHint("stage the file and run lore check leaks --staged: it prints each finding's rule and fingerprint")
	}
	return 0, errs.New(errs.InvalidInput, "secrets allow --file: fingerprint sha256:"+fingerprint+" names more than one "+rule+" finding in "+path).WithHint("give more of the fingerprint")
}
