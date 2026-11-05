// secret_scanner.go — the one constructor every check uses to build its
// credential scanner (D4).
//
// Flow (hooks, export, lore check leaks, client-repo writes, move):
//
//	runLeakCheck / secretCheck
//	  └─ newCoreSecrets(ctx, root)
//	       ├─ coreApp.HookScanner     gitleaks defaults + lore pack + the team
//	       │                          stores' secret_rules rows + D8 + rules
//	       │                          `me` adds (a personal off/change never
//	       │                          applies here: BATCH 8)
//	       └─ committedAllows         secret_allows rows from the git INDEX
//	                                  (19.4: the tree being committed, never
//	                                  the working copy alone)
//	  └─ scanItem(it)
//	       ├─ row file: each decoded field scanned by name, allow entries of
//	       │            that row id filtered (scan.Filter, every part must
//	       │            match), then the whole file text scanned once more and
//	       │            any finding the field pass did not see is kept (a value
//	       │            in the envelope or an escaped form: fail closed)
//	       └─ other text (commit message, PR body, any file): whole text, no
//	                    allow entry applies (allows are per row)
//
// With no core in the process (v0.1.17 unit tests) the old scanner of
// secretScanner stays the fallback.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/leakcheck"
	"saas/pkg/aicoder/security"
)

// piiPatternsFile is the per-user file of extra secret patterns, one regex
// per line (# comments), under ~/.lore.
const piiPatternsFile = "pii-patterns.txt"

// piiPatternsMigrated is the copy lore migrate run keeps after it turned
// the file into personal secret_rules rows (wire pii_patterns step). The
// fallback scanner (no core) keeps reading it, so a migrated pattern never
// stops blocking.
const piiPatternsMigrated = piiPatternsFile + ".migrated"

// secretScanner returns the v0.1.17 built-in credential patterns plus the
// user's own from ~/.lore/pii-patterns.txt: the fallback of every check
// when no core is mounted, and the write-time scan of the old commands.
//
// Invariant: fails closed. An unreadable file or a pattern that does not
// compile is E_CONFIG_INVALID, never a silent fall back to the built-ins.
// A missing file is fine (no extra patterns).
func secretScanner() (*security.Scanner, error) {
	s := security.NewScanner()
	home, err := loreHome()
	if err != nil {
		return nil, err
	}
	for _, name := range []string{piiPatternsFile, piiPatternsMigrated} {
		path := filepath.Join(home, name)
		if err := s.LoadUserPatterns(path); err != nil {
			return nil, errcodes.New(errcodes.ConfigInvalid, "custom secret patterns: "+err.Error()).
				WithHint("fix or remove the bad line in " + path)
		}
	}
	return s, nil
}

// allowsDir is the repo store folder of secret_allows row files.
const allowsDir = "secret_allows/"

// loreDataMark finds a lore row file in a repo path (any project).
const loreDataMark = ".lore/data/"

// coreSecrets is the core scanner plus the committed allow entries.
type coreSecrets struct {
	sc     scan.Scanner
	allows []scan.Allow
}

// newCoreSecrets builds the check scanner for the project at root (see the
// file doc). nil, nil when no core is mounted (the caller falls back). A
// core that cannot build its scanner (a bad secret_rules row, a bad
// .gitleaks.toml, an unreadable store) is an error: fail closed.
func newCoreSecrets(ctx context.Context, root string) (*coreSecrets, error) {
	if coreApp == nil {
		return nil, nil
	}
	// The not yet migrated ~/.lore/pii-patterns.txt first: a bad line
	// refuses before the core opens (fail closed, as secretScanner).
	user, err := userPatterns()
	if err != nil {
		return nil, err
	}
	sc, err := coreApp.HookScanner(ctx) // team rules only (BATCH 8)
	if err != nil {
		return nil, err
	}
	allows, err := committedAllows(ctx, root)
	if err != nil {
		return nil, err
	}
	if user != nil {
		sc = bothScanners{sc, user}
	}
	return &coreSecrets{sc: sc, allows: allows}, nil
}

// userPatternPrefix names the patterns security.LoadUserPatterns adds.
const userPatternPrefix = "user:"

// userPatterns is ~/.lore/pii-patterns.txt as a scan.Scanner, nil when the
// file is absent. Why: lore migrate run turns the file into personal
// secret_rules rows (which the core scanner already reads) and renames it
// to .migrated; until then the file's patterns must keep blocking (a user
// who wrote a pattern expects it to apply). Only the file's own patterns:
// the v0.1.17 built-ins are replaced by the core's rules.
func userPatterns() (scan.Scanner, error) {
	home, err := loreHome()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, piiPatternsFile)
	s := security.NewScanner()
	builtins := s.PatternCount()
	if err := s.LoadUserPatterns(path); err != nil { // a missing file adds none
		return nil, errcodes.New(errcodes.ConfigInvalid, "custom secret patterns: "+err.Error()).
			WithHint("fix or remove the bad line in " + path)
	}
	if s.PatternCount() == builtins {
		return nil, nil
	}
	return userScanner{s}, nil
}

// userScanner keeps only the user-file patterns of a security.Scanner.
type userScanner struct{ s *security.Scanner }

// Scan implements scan.Scanner (first match of each pattern, as v0.1.17).
func (u userScanner) Scan(field, text string) []scan.Finding {
	var out []scan.Finding
	for _, m := range u.s.Scan(text) {
		if !strings.HasPrefix(m.PatternName, userPatternPrefix) || m.Offset < 0 || m.Offset+m.Length > len(text) {
			continue
		}
		match := text[m.Offset : m.Offset+m.Length]
		out = append(out, scan.Finding{RuleID: m.PatternName, Field: field, Line: strings.Count(text[:m.Offset], "\n") + 1,
			Offset: m.Offset, Length: m.Length, Preview: scan.Mask(match), MatchHash: scan.HashMatch(match)})
	}
	return out
}

// bothScanners reports the findings of both.
type bothScanners struct{ a, b scan.Scanner }

// Scan implements scan.Scanner.
func (s bothScanners) Scan(field, text string) []scan.Finding {
	return append(s.a.Scan(field, text), s.b.Scan(field, text)...)
}

// committedAllows reads every secret_allows row file in the git index under
// the project's .lore/data/ (19.4: allow entries count only from the tree
// being committed or pushed). Not a git checkout: none. A row file that
// does not decode is skipped: an entry nobody can read allows nothing.
func committedAllows(ctx context.Context, root string) ([]scan.Allow, error) {
	top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, nil
	}
	dir := projectDataPrefix(ctx, root) + allowsDir
	out, err := exec.CommandContext(ctx, "git", "-C", top, "ls-files", "-z", "--", dir).Output()
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "list committed secret allow entries").WithCause(err)
	}
	var allows []scan.Allow
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		b, err := exec.CommandContext(ctx, "git", "-C", top, "show", ":"+string(p)).Output()
		if err != nil {
			continue
		}
		if a, ok := allowOfFile(b); ok {
			allows = append(allows, a)
		}
	}
	return allows, nil
}

// secret_allows field names (schema.Builtin).
const (
	allowFieldRowID = "row_id"
	allowFieldField = "field"
	allowFieldRule  = "rule_id"
	allowFieldHash  = "match_hash"
	maxMatchHash    = 1<<32 - 1
)

// allowOfFile reads one secret_allows row file; ok is false for a deleted,
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

// fieldText is a text field of r ("" when absent).
func fieldText(r row.Row, name string) string {
	if t, ok := r.Fields[name].(value.Text); ok {
		return t.String()
	}
	return ""
}

// scanItem scans one item (see the file doc).
func (c *coreSecrets) scanItem(it leakcheck.Item) []leakcheck.SecretHit {
	content := string(it.Content)
	if it.Path == "" || strings.Contains("/"+it.Path, "/"+loreDataMark) {
		if r, err := row.Decode(it.Content); err == nil && !r.ID.IsZero() {
			return c.scanRow(r, content)
		}
	}
	return hitsOf(content, "", c.sc.Scan("", content))
}

// findingKey identifies one secret value under one rule.
type findingKey struct {
	rule string
	hash uint32
}

// scanRow scans a decoded row file field by field, honouring the row's
// allow entries, then the whole text for anything the fields did not hold.
func (c *coreSecrets) scanRow(r row.Row, content string) []leakcheck.SecretHit {
	names := make([]string, 0, len(r.Fields))
	for n := range r.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	seen := map[findingKey]bool{}
	var out []leakcheck.SecretHit
	for _, name := range names {
		for _, text := range fieldTexts(r.Fields[name]) {
			found := c.sc.Scan(name, text)
			for _, f := range found {
				seen[findingKey{f.RuleID, f.MatchHash}] = true
			}
			kept := scan.Filter(r.ID, found, c.allows)
			line := lineOfField(content, name)
			for _, f := range kept {
				out = append(out, leakcheck.SecretHit{Line: line, Rule: f.RuleID, Preview: f.Preview, Field: name, Hash: f.MatchHash})
			}
		}
	}
	var rest []scan.Finding
	for _, f := range c.sc.Scan("", content) {
		if !seen[findingKey{f.RuleID, f.MatchHash}] {
			rest = append(rest, f)
		}
	}
	return append(out, hitsOf(content, "", rest)...)
}

// fieldTexts are the texts of a field value the write path scans: a text,
// each item of a list, the JSON form of anything else.
func fieldTexts(v value.Value) []string {
	if v == nil {
		return nil
	}
	if k := v.Kind(); k == value.KindText {
		return []string{v.String()}
	} else if k == value.KindList {
		return value.ItemStrings(v)
	}
	if b, ok := value.JSONForm(v); ok {
		return []string{string(b)}
	}
	return []string{v.String()}
}

// lineOfField is the 1-based line of a field's key in a row file (1 when
// not found).
func lineOfField(content, name string) int {
	i := strings.Index(content, `"`+name+`"`)
	if i < 0 {
		return 1
	}
	return strings.Count(content[:i], "\n") + 1
}

// hitsOf maps findings over content to hits.
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

// checkSecretScan is the leakcheck SecretScan for the project at root:
// the core scanner when a core is mounted, nil (leakcheck's own fallback)
// otherwise.
func checkSecretScan(ctx context.Context, root string) (leakcheck.SecretScan, error) {
	c, err := newCoreSecrets(ctx, root)
	if err != nil || c == nil {
		return nil, err
	}
	return c.scanItem, nil
}

// exportSecretCheck is secretCheck's core path: the scanner is built on the
// first file (a sync pass that exports nothing never opens the core) and
// reused for the pass. Any build error refuses every file with its reason.
func exportSecretCheck() func([]byte) string {
	var once sync.Once
	var c *coreSecrets
	var buildErr error
	return func(b []byte) string {
		once.Do(func() {
			root, err := os.Getwd()
			if err != nil {
				buildErr = err
				return
			}
			c, buildErr = newCoreSecrets(context.Background(), root)
		})
		if buildErr != nil {
			return buildErr.Error()
		}
		if hits := c.scanItem(leakcheck.Item{Content: b}); len(hits) > 0 {
			h := hits[0]
			if h.Field != "" {
				return "credential pattern " + h.Rule + " found in field " + h.Field
			}
			return "credential pattern " + h.Rule + " found"
		}
		return ""
	}
}

// removedAllowWarned makes warnRemovedSyncAllow print once per process.
var removedAllowWarned sync.Once

// warnRemovedSyncAllow prints one stderr warning when the removed
// LORE_SYNC_ALLOW_SECRETS is still set (D3): the scan runs and refuses
// anyway, and a person relying on the variable must learn it is ignored.
func warnRemovedSyncAllow() {
	if os.Getenv(envRemovedSyncAllow) == "" {
		return
	}
	removedAllowWarned.Do(func() {
		fmt.Fprintln(os.Stderr, "lore: warning: "+envRemovedSyncAllow+" is ignored since 0.1.18: every secret finding is refused; keep a false positive with `lore secrets allow <row> --rule <id> --reason …`")
	})
}
