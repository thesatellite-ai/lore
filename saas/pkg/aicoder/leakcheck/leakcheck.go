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
// ds:def id=team-leak-words-jpw3kpv4 owner=@khanakia stability=stable desc="words in a run the leak text rule matches"
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
	// RuleSession: a raw tool session id known on this machine appears in
	// text bound for git (AGENT_IDENTITY_DESIGN section 9, OQ14: session
	// ids are machine-local and never shared).
	RuleSession Rule = "session-id"
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
	// AllowRows names internal rows whose TEXT this item may carry because
	// it is a deliberate copy of them (`lore share`, §6.4). Only the text
	// rule honours it, and only for this item: every other internal row
	// still blocks, and the id and reference rules are unchanged.
	AllowRows []string
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
	// shingles maps a normalised MinRunWords-word window to every row id
	// whose text holds it, in index order. Every row is kept (not only the
	// first) so that allowing one row's text for a shared copy never hides
	// a second internal row that holds the same words.
	shingles map[string][]string
	// sharedFrom maps the id of a deliberately shared copy to the internal
	// row it was copied from (IndexRow.SharedAs): that copy's row file may
	// carry the source's text, nothing else may.
	sharedFrom map[string]string
}

// IndexRow is one internal row for NewIndex.
type IndexRow struct {
	ID     string
	Table  string
	Label  string
	Texts  []string
	Source string
	// SharedAs lists the ids of copies `lore share` made of this row (the
	// row's shared_as field, which only the internal side can read). The
	// row file of such a copy is allowed to carry this row's text.
	SharedAs []string
}

// NewIndex builds an Index. Actor rows feed only the actor exception, never
// the text rule (names are not secrets on their own).
func NewIndex(rows []IndexRow, projectIDs map[string]string) *Index {
	idx := &Index{IDs: map[string]string{}, ProjectIDs: map[string]string{}, actors: map[string]bool{}, shingles: map[string][]string{}, sharedFrom: map[string]string{}}
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
		for _, c := range r.SharedAs {
			idx.sharedFrom[c] = r.ID
		}
		for _, t := range r.Texts {
			words := Words(t)
			for i := 0; i+MinRunWords <= len(words); i++ {
				k := strings.Join(words[i:i+MinRunWords], " ")
				if !containsID(idx.shingles[k], r.ID) {
					idx.shingles[k] = append(idx.shingles[k], r.ID)
				}
			}
		}
	}
	return idx
}

// containsID reports whether ids holds id (shingle owner lists are short).
func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
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
	// Path is the checked item's repo-relative path ("" for a commit
	// message or PR body), so a caller can name the repo row that leaks.
	Path string
	// Detail explains it in one line.
	Detail string
	// Warn marks a finding that is reported but does not block (see
	// StableKeyBlocks).
	Warn bool
	// Secret is set on a secret-rule finding of a row field (19.4): what
	// a `lore secrets allow` entry for it names. Zero otherwise.
	Secret SecretHit
}

// StableKeyBlocks records that a person's stable_key, handle or email in a
// client repo's actor file blocks a commit; it never only warns.
//
// BATCH2 #14 (owner): the commit check blocks client-repo actor files
// carrying stable_key, handle or email, because the client copy of an
// actor is {id, display_name, kind} (F18, FLAG6) and anything more
// publishes a teammate's identity to the client; fail closed (coordinator
// ruling 18:40, BUG-D2). The finding names the file, the actor id and the
// keys (PRINCIPLE EXACT ERRORS). A correct flow never trips it: an
// explicit self-register (which keeps a handle, BATCH2 #15) writes to the
// org store's org/ folder, never to the client repo.
const StableKeyBlocks = true

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
	// Secrets runs the secret rule. SecretScan is the scanner (D4: the
	// CLI passes the core scanner with the committed allow entries,
	// saas/cmd/cli/secret_scanner.go); nil = security.NewScanner(), kept
	// only for callers with no core (v0.1.17 unit tests).
	Secrets    bool
	SecretScan SecretScan
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
	// SessionIDs are the raw tool session ids known on this machine
	// (lore.db sessions, saas/pkg/aicoder/sessions.KnownToolSessionIDs).
	// Any of them in an item blocks it (RuleSession); the finding never
	// prints the id.
	SessionIDs []string
}

// SecretHit is one secret finding in an item, safe to print: the rule id
// and a masked preview, never the value. Line is 1-based in the item.
type SecretHit struct {
	Line    int
	Rule    string
	Preview string
	// Field names the row field of a row file ("" for other text), and
	// Hash is the match hash (scan.HashMatch; safe to print, it does not
	// reveal the value), so the fix can name the `lore secrets allow`
	// entry that would cover it.
	Field string
	Hash  uint32
}

// SecretScan scans one item for secrets (Options.SecretScan).
type SecretScan func(it Item) []SecretHit

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

// Row-envelope keys every 0.1.18 row file carries (model/row, STORE_SERVER_SPEC
// 4.1). They hold bookkeeping, not content, so a client actor copy may carry
// them, but only in the shape that names nothing internal:
//   - "_hlc": the edit stamp (time/counter/writer act_ id); always allowed.
//   - "created_by", "updated_by": an act_ id (the writer; actor ids are
//     already public in a client repo, every row's created_by_actor_id is one).
//   - "_o": the provenance block (engine originOf, logic/origin
//     ClientFilter), allowed only with "store": "repo" and the client-safe
//     keys cmd, dir, repo and git {state, commit, branch} as text. PROV8:
//     a client repo keeps the commit id and state of a row born there, and a
//     branch name only when it has an upstream on the client's remote (the
//     engine drops a local-only one; this check cannot see git, so it
//     accepts the key the engine let through). Any other store name, a
//     "moves" list or another key (org, folder, plugin, import, …) would
//     tell the client about an internal place, so it blocks.
//   - "_w": the write stamp, allowed only as {"v": <text>}. Q-R3-3 / OQ7: a
//     client repo gets the lore version only, never "m": no machine id of
//     ours reaches a client.
const (
	envHLC       = "_hlc"
	envCreatedBy = "created_by"
	envUpdatedBy = "updated_by"
	envOrigin    = "_o"
	envWrite     = "_w"
	// The _o members a client copy may hold (logic/origin keys).
	originStoreKey = "store"
	originCmdKey   = "cmd"
	originDirKey   = "dir"
	originRepoKey  = "repo"
	originGitKey   = "git"
	// writeVersionKey is the only _w member a client copy may hold.
	writeVersionKey = "v"
	// repoStoreSource is the repo store's source name (store.Source "repo"):
	// the only origin a client actor copy may name.
	repoStoreSource = "repo"
)

// actorIDRe is a bare actor id (ids.go: act_ + 32 hex).
var actorIDRe = regexp.MustCompile(`^act_[0-9a-f]{32}$`)

// envelopeKeyAllowed reports whether envelope key k with value v may sit in
// a client actor copy (see the key list above). Fails closed: an unknown
// key or an unexpected shape is not allowed.
func envelopeKeyAllowed(k string, v any) bool {
	switch k {
	case envHLC:
		_, ok := v.(string)
		return ok
	case envCreatedBy, envUpdatedBy:
		s, ok := v.(string)
		return ok && actorIDRe.MatchString(s)
	case envOrigin:
		o, ok := v.(map[string]any)
		if !ok {
			return false
		}
		for ok2, ov := range o {
			switch ok2 {
			case originStoreKey:
				if ov != repoStoreSource {
					return false
				}
			case originCmdKey, originDirKey, originRepoKey:
				if _, isText := ov.(string); !isText {
					return false
				}
			case originGitKey:
				if !onlyTextKeys(ov, clientGitKeys) {
					return false
				}
			default:
				return false
			}
		}
		return true
	case envWrite:
		return onlyTextKeys(v, clientWriteKeys)
	}
	return false
}

// clientGitKeys are the _o.git members a client copy may hold (PROV8).
var clientGitKeys = map[string]bool{"state": true, "commit": true, "branch": true}

// clientWriteKeys are the _w members a client copy may hold (Q-R3-3: v
// only, never the machine id m).
var clientWriteKeys = map[string]bool{writeVersionKey: true}

// onlyTextKeys reports whether v is an object whose every member is in
// allowed and holds text. Fails closed on any other shape.
func onlyTextKeys(v any, allowed map[string]bool) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for k, mv := range m {
		if _, isText := mv.(string); !isText || !allowed[k] {
			return false
		}
	}
	return true
}

// originMovesKey / moveToKey are the move entries of _o (engine/move.go).
const (
	originMovesKey = "moves"
	moveToKey      = "moved_to"
)

// originIsRepo reports whether a 0.1.18 row's provenance block (its "_o"
// value, nil when absent) says the row belongs in the repo it sits in: it
// was created in the repo store, or its last move put it there (`lore
// move … --to repo`, a deliberate choice). A row with no _o (v0.1.17, or
// written before provenance existed) is judged by the other rules only.
//
// Why: a 0.1.18 store row carries no project_id when it has no project (a
// global memory), so CI mode's project rule cannot see it; the provenance
// says where it was written. A copy of a store's row file committed into a
// client repo names that store in _o.store, so it is caught without any
// store access, and the finding names no store.
func originIsRepo(o any) bool {
	if o == nil {
		return true
	}
	m, ok := o.(map[string]any)
	if !ok {
		return false
	}
	if moves, ok := m[originMovesKey].([]any); ok && len(moves) > 0 {
		last, ok := moves[len(moves)-1].(map[string]any)
		return ok && last[moveToKey] == repoStoreSource
	}
	return m[originStoreKey] == repoStoreSource
}

// actorFileName is the file an actor finding names: the item's path, or
// where it was found when it has none.
func actorFileName(it Item) string {
	if it.Path != "" {
		return it.Path
	}
	return it.Where
}

// actorExtraKeys lists the keys of an actor row document that a client
// repo's copy may not hold: anything outside allowedActorKeys that is not
// empty and not an allowed envelope key. Sorted, nil when the copy is clean.
func actorExtraKeys(doc map[string]any) []string {
	var extra []string
	for k, v := range doc {
		if allowedActorKeys[k] || emptyValue(v) || envelopeKeyAllowed(k, v) {
			continue
		}
		extra = append(extra, k)
	}
	sort.Strings(extra)
	return extra
}

// Check runs the id, text and secret rules over items against idx. idx may
// be nil only when the caller decided there is nothing internal loaded
// (then only the secret and project-id rules run).
func Check(items []Item, idx *Index, o Options) []Finding {
	var out []Finding
	secrets := o.SecretScan
	if o.Secrets && secrets == nil {
		secrets = legacySecretScan(security.NewScanner())
	}
	if !o.Secrets {
		secrets = nil
	}
	for _, it := range items {
		start := len(out)
		content := string(it.Content)
		if m := rowPathRe.FindStringSubmatch(it.Path); m != nil {
			out = append(out, checkRowFile(it, m[1], m[2], idx, o)...)
		} else if idx != nil {
			out = append(out, idHits(it, content, idx)...)
		}
		if idx != nil {
			out = append(out, textHits(it, content, idx, allowedRows(it, idx))...)
		}
		out = append(out, sessionHits(it, content, o.SessionIDs)...)
		if secrets != nil {
			for _, h := range secrets(it) {
				detail := "credential pattern " + h.Rule + " (" + h.Preview + ")"
				if h.Field != "" {
					detail = "credential pattern " + h.Rule + " in field " + h.Field + " (" + h.Preview + ")"
				}
				f := Finding{Rule: RuleSecret, Where: it.Where, Line: h.Line, Detail: detail}
				if h.Field != "" {
					f.Secret = h
				}
				out = append(out, f)
			}
		}
		for i := start; i < len(out); i++ {
			out[i].Path = it.Path
		}
	}
	sortFindings(out)
	return out
}

// legacySecretScan is the v0.1.17 scanner as a SecretScan (whole content).
func legacySecretScan(sc *security.Scanner) SecretScan {
	return func(it Item) []SecretHit {
		content := string(it.Content)
		var out []SecretHit
		for _, m := range sc.Scan(content) {
			out = append(out, SecretHit{Line: lineOf(content, m.Offset), Rule: m.PatternName, Preview: m.Preview})
		}
		return out
	}
}

// sessionHits finds every known tool session id in content (section 9).
// The detail names the rule, never the id: the finding itself may be
// printed into a CI log.
func sessionHits(it Item, content string, sessionIDs []string) []Finding {
	var out []Finding
	for _, id := range sessionIDs {
		if id == "" {
			continue
		}
		if off := strings.Index(content, id); off >= 0 {
			out = append(out, Finding{Rule: RuleSession, Where: it.Where, Line: lineOf(content, off),
				Detail: "an agent's tool session id (machine-local, never shared; remove it from the text)"})
		}
	}
	return out
}

// allowedRows is the set of internal rows whose text item may carry: the
// caller's AllowRows plus, for the row file of a deliberately shared copy,
// the row it was copied from (Index.sharedFrom).
func allowedRows(it Item, idx *Index) map[string]bool {
	out := map[string]bool{}
	for _, id := range it.AllowRows {
		out[id] = true
	}
	if m := rowPathRe.FindStringSubmatch(it.Path); m != nil {
		if src, ok := idx.sharedFrom[m[2]]; ok {
			out[src] = true
		}
	}
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
		if extra := actorExtraKeys(doc); len(extra) > 0 {
			// BATCH2 #14: every extra key blocks (StableKeyBlocks), and the
			// reason names the file, the actor id and the keys.
			out = append(out, Finding{Rule: RuleActor, Where: it.Where, RowID: id, Path: it.Path, Warn: !StableKeyBlocks,
				Detail: "actor " + id + " in client-repo file " + actorFileName(it) + " carries " + strings.Join(extra, ", ") +
					": a client repo may hold only {id, display name, kind} of an actor (privacy rule); remove those keys from the file"})
		}
		return out
	}
	if o.ClientRepo && !originIsRepo(doc[envOrigin]) {
		out = append(out, Finding{Rule: RuleID, Where: it.Where, RowID: id, Detail: "row written in another store (its provenance is not this repo's)"})
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
// Rows in allowed are skipped (a deliberate shared copy of them).
func textHits(it Item, content string, idx *Index, allowed map[string]bool) []Finding {
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
		for _, row := range idx.shingles[strings.Join(parts, " ")] {
			if allowed[row] {
				continue
			}
			n := MinRunWords
			run := append([]string(nil), parts...)
			for i+n < len(words) {
				run = append(run[1:], words[i+n].w)
				if !containsID(idx.shingles[strings.Join(run, " ")], row) {
					break
				}
				n++
			}
			if f, ok := best[row]; !ok || n > f.Words {
				best[row] = Finding{Rule: RuleText, Where: it.Where, Line: words[i].line, RowID: row, Label: idx.IDs[row], Words: n,
					Detail: fmt.Sprintf("matches internal note %s (%d words)", row, n)}
			}
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
			// BATCH2 #14: name the keys (exact reason); the id stays
			// withheld as for every CI finding.
			if extra := actorExtraKeys(doc); len(extra) > 0 {
				out = append(out, CIFinding{Where: where, Detail: "an actor copy holds more than {id, display name, kind}: " + strings.Join(extra, ", ")})
			}
			continue
		}
		if pid, ok := doc["project_id"].(string); ok && pid != "" && pid != repoProjectID {
			out = append(out, CIFinding{Where: where, Detail: "belongs to another project (its project id is not this repo's)"})
		}
		if !originIsRepo(doc[envOrigin]) {
			out = append(out, CIFinding{Where: where, Detail: "was written in another store (its provenance is not this repo's)"})
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
