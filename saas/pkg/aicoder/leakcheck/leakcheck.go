// Package leakcheck decides whether content about to reach something a
// client can read carries internal or personal content (LORE_TEAM_SPEC
// F16, §6.3, ADR TEAM-024). It is pure: callers hand it the items (staged
// files, a commit message, a live write, a landing commit, a PR body) and
// an Index of what is internal, and get findings back.
//
// Three rules, in the spec's words:
//
//  1. id rule — a row whose id belongs to another project, or content that
//     names an internal or personal row id (a pointer alone reveals the
//     hidden row, F15). Exception: actor files a client row references,
//     copied as the allow-listed projection {id, display name, kind}.
//  2. text rule — after normalising (lowercase, Unicode NFKC, punctuation
//     stripped, whitespace collapsed), any run of at least MinRunWords
//     consecutive words that also appears in an internal or personal row.
//  3. secret rule — the credential scanner (security, §6.5 extended set).
//
// CheckCI is the client CI's variant: no index (the CI cannot read the
// store), structure only, and its findings never name an internal id,
// store or org.
//
// This is a copy-paste catch, not a wall (§6.6): paraphrase, --no-verify
// and the web UI pass it. Callers must say so where it matters.
package leakcheck

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"saas/pkg/aicoder/security"
)

// MinRunWords is the text rule's threshold (F16: "any run of ≥ 8
// consecutive words"). Seven matching words are allowed: short shared
// phrases ("send the estimate before the call") are common English.
const MinRunWords = 8

// Rule names a finding's rule.
type Rule string

// Rules.
const (
	RuleID     Rule = "id"
	RuleRef    Rule = "reference"
	RuleText   Rule = "text"
	RuleSecret Rule = "secret"
	RuleActor  Rule = "actor-projection"
)

// Item is one thing to check.
type Item struct {
	// Where names it in findings: a repo-relative path, "commit message",
	// "PR body", "live write".
	Where string
	// Path is the repo-relative path for files ("" otherwise); a path under
	// .lore/data/<table>/<id>.json is a row file.
	Path    string
	Content []byte
}

// Index is what the id and text rules compare against: every internal and
// personal row loaded for this repo.
type Index struct {
	// IDs maps an internal or personal row id to its source label
	// ([northwind/acme], [me]).
	IDs map[string]string
	// ProjectIDs are the ids of internal and personal projects.
	ProjectIDs map[string]string
	// actors are roster ids: a client row may reference them (projection).
	actors map[string]bool
	// shingles maps a normalised MinRunWords-word window to the row id
	// whose text holds it.
	shingles map[string]string
}

// IndexRow is one internal row for NewIndex.
type IndexRow struct {
	ID     string
	Table  string
	Label  string
	Texts  []string
	Source string
}

// NewIndex builds an Index. Actor rows feed only the actor exception, never
// the text rule (names are not secrets on their own).
func NewIndex(rows []IndexRow, projectIDs map[string]string) *Index {
	idx := &Index{IDs: map[string]string{}, ProjectIDs: map[string]string{}, actors: map[string]bool{}, shingles: map[string]string{}}
	for k, v := range projectIDs {
		idx.ProjectIDs[k] = v
	}
	for _, r := range rows {
		if r.Table == tableActors || r.Table == tableTeams {
			idx.actors[r.ID] = true
			continue
		}
		if r.Table == tableProjects {
			continue
		}
		idx.IDs[r.ID] = r.Label
		for _, t := range r.Texts {
			words := Words(t)
			for i := 0; i+MinRunWords <= len(words); i++ {
				k := strings.Join(words[i:i+MinRunWords], " ")
				if _, ok := idx.shingles[k]; !ok {
					idx.shingles[k] = r.ID
				}
			}
		}
	}
	return idx
}

// Tables with special handling.
const (
	tableActors   = "actors"
	tableTeams    = "teams"
	tableProjects = "projects"
)

// Words normalises text into words: NFKC, lowercase, every rune that is
// not a letter or digit is a separator.
func Words(s string) []string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(s)
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// Finding is one reason to block.
type Finding struct {
	Rule  Rule
	Where string
	// Line is 1-based for text and id hits in a file (0 = not located).
	Line int
	// RowID is the internal row matched (text, id, reference rules).
	RowID string
	// Label is the internal row's source label.
	Label string
	// Words is the length of the matched run (text rule).
	Words int
	// Detail explains it in one line.
	Detail string
	// Warn marks a finding that is reported but does not block (see
	// StableKeyBlocks).
	Warn bool
}

// StableKeyBlocks decides whether a person's stable_key in a client repo's
// actor file blocks a commit. The spec's client copy is {id, display name,
// kind} (F18), but until the export writes that projection (lore-live, C15
// follow-up) every client-repo actor file carries its stable_key, and
// blocking would stop every commit of every org member. So it is reported
// as a warning; agent details (owner_actor_id, label — D8) always block.
// Flip to true once the export projects actors.
const StableKeyBlocks = false

// warnOnlyActorKeys are the actor columns that only warn while
// StableKeyBlocks is false.
var warnOnlyActorKeys = map[string]bool{"stable_key": true, "handle": true}

// Blocking returns the findings that block (Warn unset).
func Blocking(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if !f.Warn {
			out = append(out, f)
		}
	}
	return out
}

// Options tune Check.
type Options struct {
	// Secrets runs the secret rule (nil scanner = security.NewScanner()).
	Secrets bool
	Scanner *security.Scanner
	// ClientRepo turns on the actor projection rule: in a client repo an
	// actor file may hold only {id, display name, kind} (F18 client copy);
	// in any other repo actor files are the repo's own business.
	ClientRepo bool
	// DataPrefix is this project's repo-relative ".lore/data/" path
	// ("web/.lore/data/" in a monorepo). The project-id rule applies only
	// to row files under it: another project of the same repo checks its
	// own rows from its own hook block. "" = every row file.
	DataPrefix string
	// RepoProjectID is this repo's project id; a row file carrying another
	// project id is an id-rule finding even when the index does not know
	// that id (copied from an unloaded store).
	RepoProjectID string
}

// idRe finds lore row ids in any text (prefix_ + 32 hex, ids.go).
var idRe = regexp.MustCompile(`\b[a-z]{3}_[0-9a-f]{32}\b`)

// rowPathRe recognises .lore/data/<table>/<id>.json (any project folder).
var rowPathRe = regexp.MustCompile(`(?:^|/)\.lore/data/([a-z_]+)/([a-z]{3}_[0-9a-f]{32})\.json$`)

// allowedActorKeys is the projection a client repo may hold for an actor a
// client row references (F16 exception, F18 client copy).
var allowedActorKeys = map[string]bool{
	"_table": true, "_v": true, "id": true, "display_name": true, "name": true, "kind": true,
	"created_at": true, "updated_at": true, "archived_at": true, "active": true,
}

// Check runs the id, text and secret rules over items against idx. idx may
// be nil only when the caller decided there is nothing internal loaded
// (then only the secret and project-id rules run).
func Check(items []Item, idx *Index, o Options) []Finding {
	var out []Finding
	scanner := o.Scanner
	if o.Secrets && scanner == nil {
		scanner = security.NewScanner()
	}
	for _, it := range items {
		content := string(it.Content)
		if m := rowPathRe.FindStringSubmatch(it.Path); m != nil {
			out = append(out, checkRowFile(it, m[1], m[2], idx, o)...)
		} else if idx != nil {
			out = append(out, idHits(it, content, idx)...)
		}
		if idx != nil {
			out = append(out, textHits(it, content, idx)...)
		}
		if scanner != nil {
			for _, s := range scanner.Scan(content) {
				out = append(out, Finding{Rule: RuleSecret, Where: it.Where, Line: lineOf(content, s.Offset),
					Detail: "credential pattern " + s.PatternName + " (" + s.Preview + ")"})
			}
		}
	}
	sortFindings(out)
	return out
}

// checkRowFile applies the id rule to one row file.
func checkRowFile(it Item, table, id string, idx *Index, o Options) []Finding {
	var out []Finding
	var doc map[string]any
	_ = json.Unmarshal(it.Content, &doc) // a broken row file is the sync check's business
	if idx != nil {
		if label, ok := idx.IDs[id]; ok {
			out = append(out, Finding{Rule: RuleID, Where: it.Where, RowID: id, Label: label, Detail: "id belongs to " + label})
		}
	}
	if table == tableActors {
		if !o.ClientRepo {
			return out
		}
		var extra []string
		for k, v := range doc {
			if !allowedActorKeys[k] && !emptyValue(v) {
				extra = append(extra, k)
			}
		}
		if len(extra) > 0 {
			sort.Strings(extra)
			warn := !StableKeyBlocks
			for _, k := range extra {
				if !warnOnlyActorKeys[k] {
					warn = false
				}
			}
			out = append(out, Finding{Rule: RuleActor, Where: it.Where, RowID: id, Warn: warn,
				Detail: "an actor copy in a client repo may hold only {id, display name, kind}; also has " + strings.Join(extra, ", ")})
		}
		return out
	}
	if pid, ok := doc["project_id"].(string); ok && o.RepoProjectID != "" && pid != "" && pid != o.RepoProjectID && (o.DataPrefix == "" || strings.HasPrefix(it.Path, o.DataPrefix)) {
		label := ""
		if idx != nil {
			label = idx.ProjectIDs[pid]
		}
		out = append(out, Finding{Rule: RuleID, Where: it.Where, RowID: id, Label: label, Detail: "row of another project"})
	}
	if idx != nil {
		for _, ref := range refsIn(doc) {
			if ref == id {
				continue
			}
			if label, ok := idx.IDs[ref]; ok {
				out = append(out, Finding{Rule: RuleRef, Where: it.Where, RowID: ref, Label: label, Detail: "refers to a row of " + label + " (pointers flow inward only, F15)"})
			}
		}
		for _, pid := range projectRefsIn(doc) {
			if label, ok := idx.ProjectIDs[pid]; ok {
				out = append(out, Finding{Rule: RuleRef, Where: it.Where, Label: label, Detail: "points into " + label + " (pointers flow inward only, F15)"})
			}
		}
	}
	return out
}

// emptyValue: a key present with null, "" or false carries no information
// (the engine writes every column; an unset handle is not a leak).
func emptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	}
	return false
}

// refsIn returns every row id appearing as a string value anywhere in doc.
func refsIn(doc map[string]any) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			out = append(out, idRe.FindAllString(x, -1)...)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)
	return out
}

// projectRefKeys are the reference fields that name another project (C16).
var projectRefKeys = []string{"entity_project_id", "dst_project_id"}

func projectRefsIn(doc map[string]any) []string {
	var out []string
	for _, k := range projectRefKeys {
		if s, ok := doc[k].(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// idHits finds internal ids named in any other content (source, message).
func idHits(it Item, content string, idx *Index) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, loc := range idRe.FindAllStringIndex(content, -1) {
		id := content[loc[0]:loc[1]]
		label, ok := idx.IDs[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Finding{Rule: RuleID, Where: it.Where, Line: lineOf(content, loc[0]), RowID: id, Label: label, Detail: "names a row of " + label})
	}
	return out
}

// textHits finds runs of ≥ MinRunWords words shared with an internal row:
// one finding per (item, internal row), with the longest run's length.
func textHits(it Item, content string, idx *Index) []Finding {
	if len(idx.shingles) == 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	type wordPos struct {
		w    string
		line int
	}
	var words []wordPos
	for i, l := range lines {
		for _, w := range Words(l) {
			words = append(words, wordPos{w, i + 1})
		}
	}
	best := map[string]Finding{}
	for i := 0; i+MinRunWords <= len(words); i++ {
		parts := make([]string, MinRunWords)
		for j := 0; j < MinRunWords; j++ {
			parts[j] = words[i+j].w
		}
		row, ok := idx.shingles[strings.Join(parts, " ")]
		if !ok {
			continue
		}
		n := MinRunWords
		for i+n < len(words) {
			parts = append(parts[1:], words[i+n].w)
			if idx.shingles[strings.Join(parts, " ")] != row {
				break
			}
			n++
		}
		if f, ok := best[row]; !ok || n > f.Words {
			best[row] = Finding{Rule: RuleText, Where: it.Where, Line: words[i].line, RowID: row, Label: idx.IDs[row], Words: n,
				Detail: fmt.Sprintf("matches internal note %s (%d words)", row, n)}
		}
	}
	out := make([]Finding, 0, len(best))
	for _, f := range best {
		out = append(out, f)
	}
	return out
}

func lineOf(s string, off int) int {
	if off < 0 || off > len(s) {
		return 0
	}
	return strings.Count(s[:off], "\n") + 1
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Where != fs[j].Where {
			return fs[i].Where < fs[j].Where
		}
		if fs[i].Line != fs[j].Line {
			return fs[i].Line < fs[j].Line
		}
		return fs[i].RowID < fs[j].RowID
	})
}

// CIFinding is a CI-mode finding: it names the place and the kind of
// problem only — never an internal id, store, org or folder.
type CIFinding struct {
	// Where is "<table> row file" or a reference field description; the
	// row id itself is withheld (it may be an internal id).
	Where  string
	Detail string
}

// CheckCI is the client CI's check (F16 CI mode): it needs no store. For
// every row file under .lore/data it checks that the file's project_id and
// reference project fields are this repo's (repoProjectID) and that actor
// files hold only the allowed projection. files maps repo-relative paths
// to content.
func CheckCI(files map[string][]byte, repoProjectID string) []CIFinding {
	var out []CIFinding
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		m := rowPathRe.FindStringSubmatch(p)
		if m == nil {
			continue
		}
		table := m[1]
		var doc map[string]any
		if err := json.Unmarshal(files[p], &doc); err != nil {
			continue
		}
		where := table + " row file"
		if table == tableActors {
			for k, v := range doc {
				if !allowedActorKeys[k] && !emptyValue(v) {
					out = append(out, CIFinding{Where: where, Detail: "an actor copy holds more than {id, display name, kind}"})
					break
				}
			}
			continue
		}
		if pid, ok := doc["project_id"].(string); ok && pid != "" && pid != repoProjectID {
			out = append(out, CIFinding{Where: where, Detail: "belongs to another project (its project id is not this repo's)"})
		}
		for _, k := range projectRefKeys {
			if s, ok := doc[k].(string); ok && s != "" && s != repoProjectID {
				out = append(out, CIFinding{Where: where, Detail: k + " points outside this repo"})
			}
		}
	}
	return out
}

// NeedsIndex reports whether the outcome of Check over items can depend on
// the Index: true for any file (a staged file is always checked against
// the store, F16), and for text that has an id-shaped token or at least
// MinRunWords words. A caller that cannot build the index fails closed
// only when this is true — an empty commit with a one-word message cannot
// leak anything, whatever the store holds.
func NeedsIndex(items []Item) bool {
	for _, it := range items {
		if it.Path != "" {
			return true
		}
		s := string(it.Content)
		if idRe.MatchString(s) || len(Words(s)) >= MinRunWords {
			return true
		}
	}
	return false
}
