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
//	  ─▶ E_LEAK_BLOCKED naming file, line and rule, never the value
//
// Fails closed: a scanner or allow list that cannot be built refuses.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// secret_allows row fields and folder (schema.Builtin).
const (
	allowsDirRel    = ".lore/data/secret_allows/"
	allowFieldRowID = "row_id"
	allowFieldField = "field"
	allowFieldRule  = "rule_id"
	allowFieldHash  = "match_hash"
	maxMatchHash    = 1<<32 - 1
	loreDataMark    = ".lore/data/"
)

// HookSecrets scans docs for credentials before a commit or push from the
// repository at root; where names the action ("commit", "push").
func (a *App) HookSecrets(ctx context.Context, root, where string, docs map[string][]byte) error {
	if len(docs) == 0 {
		return nil
	}
	sc, err := a.HookScanner(ctx)
	if err != nil {
		return errs.Wrap(errs.LeakBlocked, "cannot check for secrets: the scanner could not be built", err)
	}
	allows, err := committedAllows(ctx, root)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(docs))
	for p := range docs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var lines, refused []string
	for _, p := range paths {
		for _, h := range scanDoc(sc, allows, p, docs[p]) {
			at := fmt.Sprintf("%s:%d", p, h.Line)
			if h.Field != "" {
				at += " (field " + h.Field + ")"
			}
			lines = append(lines, fmt.Sprintf("  %-40s secret: rule %s matched %s", at, h.Rule, h.Preview))
			refused = append(refused, "path="+p+" rule="+h.Rule)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	a.auditHookRefused(ctx, where, refused)
	return errs.New(errs.LeakBlocked, "blocked, this "+where+" carries a secret:\n"+strings.Join(lines, "\n")).
		WithHint("store the value in your vault and reference it (${secret:…}); if it is not a secret: lore secrets allow <row id> --rule <rule> --field <field> --match-hash <hash> --reason \"<why>\", add the new .lore/data/secret_allows file, then commit again")
}

// committedAllows reads every secret_allows row file in the git index under
// root's .lore/data (19.4: only the tree being committed counts). Not a git
// checkout: none. A file that does not decode allows nothing.
func committedAllows(ctx context.Context, root string) ([]scan.Allow, error) {
	top := gitTop(ctx, root)
	if top == "" {
		return nil, nil
	}
	out, err := gitRun{dir: root, args: []string{"ls-files", "-z", "--full-name", "--", allowsDirRel}}.output(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "list committed secret allow entries", err)
	}
	var allows []scan.Allow
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		b, serr := gitRun{dir: top, args: []string{"show", ":" + p}}.output(ctx)
		if serr != nil {
			continue
		}
		if al, ok := allowOfFile(b); ok {
			allows = append(allows, al)
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
	target, err := ids.ParseAny(fieldText(r, allowFieldRowID))
	if err != nil {
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
	return scan.Allow{RowID: target, Field: fieldText(r, allowFieldField), RuleID: fieldText(r, allowFieldRule), MatchHash: uint32(h)}, true
}

// scanDoc scans one document (see the file comment).
func scanDoc(sc scan.Scanner, allows []scan.Allow, p string, b []byte) []leakcheck.SecretHit {
	content := string(b)
	if strings.Contains("/"+p, "/"+loreDataMark) {
		if r, err := row.Decode(b); err == nil && !r.ID.IsZero() {
			return scanRowDoc(sc, allows, r, content)
		}
	}
	// The path is the field of a plain document, so path-scoped filters
	// (a go.sum module hash) can tell the file; hits keep no field.
	return hitsOf(content, "", sc.Scan(p, content))
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
			for _, f := range scan.Filter(r.ID, found, allows) {
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
