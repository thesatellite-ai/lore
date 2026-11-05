package wire

// leakdocs.go — the F16 leak check of content about to reach something the
// client can read, from the core's own stores (LORE_TEAM_SPEC F16, §6.3;
// ported from saas/cmd/cli check_cmd.go leakCheckDocs + leak_core_index.go
// for the binary built from this module, P3-MODEB-5).
//
// Flow:
//
//	org situation ── own repo (link known) ─▶ nothing to check
//	              └─ client repo, or link unknown ─▶ index:
//	                   every attached store of class personal (always) or
//	                   internal (client repo only), not shareable, every
//	                   core table; the org stores the link names must be
//	                   attached, else "cannot check"
//	logic/leakcheck.Check(id, text, ref, metadata rules) ─▶ E_LEAK_BLOCKED
//
// Fails closed: a store or table that cannot be read, or a link that cannot
// be resolved, refuses with E_LEAK_BLOCKED "cannot check", never passes.
//
// The secret rule is not run here: every row reaching a live write or a
// landing went through the engine's own secret scan with the row's allow
// entries (SECRETS 4) when it was written; re-scanning the bare document
// without them would refuse a row the person allowed.

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// leakCannotCheck is the refusal when the internal rows cannot be read.
const leakCannotCheck = "cannot check: store unreachable and nothing cached"

// leakTextFields are the prose fields of core rows the text rule compares
// against. Only prose: a field holding ids would make an internal row that
// legitimately points at a client row match the client row's own id.
var leakTextFields = []string{"title", "name", "body", "description", "reason", "notes", "message"}

// leakStoreLabelPrefix starts the label a finding names for a row's store.
const leakStoreLabelPrefix = "store "

// Key row fields the leak index reads (schema meta_keys).
const (
	leakKeyName      = "name"
	leakKeyScope     = "scope"
	leakKeyType      = "type"
	leakKeyInternal  = "internal"
	leakKeySensitive = "sensitive"
)

// LeakCheckDocs runs F16 over docs (repo-relative path → bytes) about to be
// written where the client of this repo can read them; where names the
// action in the refusal ("landing", "live write"). res is the binary's org
// resolution (StandaloneOptionsOrg).
func (a *App) LeakCheckDocs(ctx context.Context, res *OrgResolution, where string, docs map[string][]byte) error {
	if len(docs) == 0 || res == nil {
		return nil
	}
	s, err := res.Situation(ctx)
	if err != nil {
		return errs.Wrap(errs.LeakBlocked, leakCannotCheck, err)
	}
	if s.ResolveErr != nil {
		return errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (the org of this repo: "+s.ResolveErr.Error()+")", s.ResolveErr)
	}
	client := s.Result.Role == orgresolve.RoleClient
	if (s.Result.Role == orgresolve.RoleOwn && !s.Result.LinkUnknown) || s.ProjectID == "" {
		return nil // an own repo with a known link (or no project) is not checked
	}
	expected, err := res.OrgStores(ctx)
	if err != nil {
		return errs.Wrap(errs.LeakBlocked, leakCannotCheck, err)
	}
	names := make([]store.Source, 0, len(expected))
	for _, e := range expected {
		names = append(names, e.Name)
	}
	rows, err := a.leakRows(ctx, client, names)
	if err != nil {
		return err
	}
	if client {
		keys, kerr := a.List(ctx, store.SourceRepo, schema.TableMetaKeys)
		if kerr != nil {
			return errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (the repo's key rows)", kerr)
		}
		rows = withRepoKeys(rows, keys)
	}
	items := make([]leakcheck.Item, 0, len(docs))
	for p, b := range docs {
		items = append(items, leakcheck.Item{Where: where + " " + p, Path: p, Content: b})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	fs := leakcheck.Blocking(a.leakCheckByProject(ctx, items, leakcheck.NewIndex(rows, nil), leakcheck.Options{RepoProjectID: s.ProjectID, ClientRepo: client})) // per monorepo project folder (leakdocs_projects.go)
	fs, moved := withoutSelfMatches(fs, rows)
	if len(fs) == 0 {
		return nil
	}
	if moved {
		return leakBlocked(leakWhereMove, fs)
	}
	return leakBlocked(where, fs)
}

// leakWhereMove names a write that carries an internal row to the client
// under its own id (`lore move <id> --to repo`).
const leakWhereMove = "move"

// withoutSelfMatches drops findings where a row file matches the internal
// row of its own id: that row is being moved on purpose (`lore move` keeps
// the id, shows the text and asks), which is not a leak of another note.
// moved reports whether any checked file is such a move; its remaining
// findings (it also quotes another note) get the move's advice.
func withoutSelfMatches(fs []leakcheck.Finding, rows []leakcheck.IndexRow) ([]leakcheck.Finding, bool) {
	internal := make(map[string]bool, len(rows))
	for _, r := range rows {
		if !r.RepoKey {
			internal[r.ID] = true
		}
	}
	out := fs[:0:0]
	moved := false
	for _, f := range fs {
		id, _, isRow := repoRowOf(f.Path)
		if isRow && internal[id] {
			moved = true
			if f.RowID == id {
				continue
			}
		}
		out = append(out, f)
	}
	return out, moved
}

// leakBlocked is the F16 refusal: one line per finding, the fix as hint
// (one per finding, by action). A text finding names the internal note it
// matched by id, never its text.
func leakBlocked(where string, fs []leakcheck.Finding) error {
	var b strings.Builder
	fmt.Fprintf(&b, "blocked, this %s would share internal content with the client repo:", where)
	var hints []string
	seen := map[string]bool{}
	for _, f := range fs {
		detail := f.Detail
		if f.Rule == leakcheck.RuleText {
			detail = fmt.Sprintf("matches internal note %s (%d words)", f.RowID, f.Words)
		}
		fmt.Fprintf(&b, "\n  %-40s %s", f.Where, detail)
		if h := leakFix(where, f.Path); !seen[h] {
			seen[h] = true
			hints = append(hints, h)
		}
	}
	if leakWritesNothing[where] {
		b.WriteString("\nNothing written.")
	}
	return errs.New(errs.LeakBlocked, b.String()).WithHint(strings.Join(hints, "; "))
}

// Leak check actions the hooks and hosts name (the refusal says "this <where>").
const (
	LeakWherePush   = "push"
	LeakWhereCommit = checkWhereCommit
)

// leakWritesNothing are the actions refused before anything is written.
var leakWritesNothing = map[string]bool{"live write": true, "repo write": true, leakWhereMove: true}

// leakFix is the advice for one finding at path (v0.1.17 check_cmd.go
// leakFix): a repo row is moved to the org or deleted; a pushed commit is
// rewritten; any other file has the text removed.
func leakFix(where, path string) string {
	id, table, isRow := repoRowOf(path)
	switch where {
	case LeakWherePush:
		if isRow {
			return fmt.Sprintf("%s (a %s in this repo) is in a commit you are pushing: rewrite that commit without it (git commit --amend, or git rebase), then keep it internal: lore move %s --to <org>", id, singularKind(table), id)
		}
		return "Remove the internal text from " + path + " and rewrite the commit(s) that carry it (git commit --amend, or git rebase) before pushing"
	case LeakWhereCommit:
		if isRow {
			k := singularKind(table)
			return fmt.Sprintf("%s is a %s in this repo. Keep it internal: lore move %s --to <org>   or remove it: lore %s delete %s --confirm", id, k, id, k, id)
		}
		return "Remove the internal text from " + path
	}
	if where == leakWhereMove && isRow {
		return fmt.Sprintf("Not moved: %s carries text of an internal note named above, which stays internal. Remove that part from %s, then move it again; nothing written", id, id)
	}
	return "move the row to the org (`lore move <id> --to <org>`) or delete it; the rows stay on the live ref until then"
}

// repoRowPathRe matches a row file of this repo's .lore/data (any project
// folder of a monorepo): .lore/data/<table>/<id>.json.
var repoRowPathRe = regexp.MustCompile(`(?:^|/)\.lore/data/([a-z_]+)/([a-z]{3}_[0-9a-f]{32})\.json$`)

// repoRowOf is the row id and table of a repo row file path.
func repoRowOf(path string) (id, table string, ok bool) {
	m := repoRowPathRe.FindStringSubmatch(path)
	if m == nil {
		return "", "", false
	}
	return m[2], m[1], true
}

// singularKind is the command noun of a table ("memories" → "memory").
func singularKind(table string) string {
	if k, ok := strings.CutSuffix(table, "ies"); ok {
		return k + "y"
	}
	return strings.TrimSuffix(table, "s")
}

// leakReader is what the leak index reads of the App: the attached stores,
// their classes and their rows. *App is the one implementation; tests pass
// a reader whose calls fail, to prove every read failure refuses.
type leakReader interface {
	StoreIdentities(ctx context.Context) ([]StoreIdentity, error)
	StoreClass(ctx context.Context, name store.Source) (direction.Class, error)
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
}

var _ leakReader = (*App)(nil)

// leakRows is the index rows of every attached store internal to this repo
// (see the file comment); expected stores must be attached.
func (a *App) leakRows(ctx context.Context, client bool, expected []store.Source) ([]leakcheck.IndexRow, error) {
	return leakRowsFrom(ctx, a, client, expected)
}

// leakRowsFrom is leakRows over any leakReader; a read that fails refuses
// (E_LEAK_BLOCKED "cannot check"), never counts as empty.
func leakRowsFrom(ctx context.Context, a leakReader, client bool, expected []store.Source) ([]leakcheck.IndexRow, error) {
	idents, err := a.StoreIdentities(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck, err)
	}
	attached := make(map[store.Source]bool, len(idents))
	for _, si := range idents {
		attached[si.Name] = true
	}
	for _, name := range expected {
		if !attached[name] {
			return nil, errs.New(errs.LeakBlocked, leakCannotCheck+" (store "+string(name)+")")
		}
	}
	var out []leakcheck.IndexRow
	for _, si := range idents {
		if si.Shareable {
			continue
		}
		class, err := a.StoreClass(ctx, si.Name)
		if err != nil {
			return nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck, err)
		}
		if !leakIndexesClass(class, client) {
			continue
		}
		label := leakStoreLabelPrefix + string(si.Name)
		keyRows, err := a.List(ctx, si.Name, schema.TableMetaKeys)
		if err != nil {
			return nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (store "+string(si.Name)+")", err)
		}
		keys := leakKeysOf(keyRows)
		for _, t := range CoreTables() {
			rows, err := a.List(ctx, si.Name, t)
			if err != nil {
				return nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (store "+string(si.Name)+")", err)
			}
			out = append(out, leakIndexRows(rows, t, si.Name, label, keys)...)
		}
	}
	return out, nil
}

// leakIndexesClass: personal stores always, internal ones seen from a
// client repo only, never repo or client-class stores.
func leakIndexesClass(class direction.Class, client bool) bool {
	switch class {
	case direction.Personal:
		return true
	case direction.Internal:
		return client
	case direction.Client:
		return false
	}
	return false // the repo store, or a class this binary does not know
}

// withRepoKeys adds the repo's live key rows as RepoKey index rows (the
// foreign-key rule) and drops their names from the internal key names (a
// key name the client repo defines itself is not a leak there).
func withRepoKeys(out []leakcheck.IndexRow, repoKeys []row.Row) []leakcheck.IndexRow {
	own := map[string]bool{}
	for _, k := range repoKeys {
		if k.Deleted {
			continue
		}
		own[fieldText(k, leakKeyName)] = true
		out = append(out, leakcheck.IndexRow{ID: k.ID.String(), Table: string(schema.TableMetaKeys), Source: string(store.SourceRepo),
			RepoKey: true, RepoKeyScope: fieldText(k, leakKeyScope)})
	}
	for i := range out {
		if len(out[i].KeyNames) > 0 {
			out[i].KeyNames = slices.DeleteFunc(slices.Clone(out[i].KeyNames), func(n string) bool { return own[n] })
		}
	}
	return out
}

// leakKey is what the metadata leak rules need of one key row.
type leakKey struct {
	typ                 string
	internal, sensitive bool
}

// leakKeysOf maps a store's live key rows by id.
func leakKeysOf(rows []row.Row) map[string]leakKey {
	out := make(map[string]leakKey, len(rows))
	for _, k := range rows {
		if !k.Deleted {
			out[k.ID.String()] = leakKey{typ: fieldText(k, leakKeyType), internal: fieldBool(k, leakKeyInternal), sensitive: fieldBool(k, leakKeySensitive)}
		}
	}
	return out
}

// fieldBool is a bool field of r (false when absent).
func fieldBool(r row.Row, f string) bool {
	b, ok := r.Fields[f].(value.Bool)
	return ok && b.Bool()
}

// leakIndexRows turns live rows of one table into index rows: prose
// fields and metadata as text, internal or sensitive metadata values as
// tokens (an unknown key counts as internal), an internal key row's name.
// Tombstones carry no text and are skipped.
func leakIndexRows(rows []row.Row, t row.Table, src store.Source, label string, keys map[string]leakKey) []leakcheck.IndexRow {
	out := make([]leakcheck.IndexRow, 0, len(rows))
	for _, r := range rows {
		if r.Deleted {
			continue
		}
		var texts []string
		for _, f := range leakTextFields {
			if s := fieldText(r, f); s != "" {
				texts = append(texts, s)
			}
		}
		var tokens []leakcheck.Token
		for kid, j := range r.Meta {
			en, err := meta.DecodeEntry(j)
			if err != nil {
				continue // lint reports an entry that does not decode
			}
			k, known := keys[kid.String()]
			secretish := !known || k.internal || k.sensitive || en.Sensitive
			for _, v := range meta.Strings(en.V) {
				if v == "" {
					continue
				}
				texts = append(texts, v, en.Name+": "+v)
				if secretish {
					tokens = append(tokens, leakcheck.Token{Value: v, Key: en.Name, Type: k.typ})
				}
			}
		}
		sort.Strings(texts)
		slices.SortFunc(tokens, func(a, b leakcheck.Token) int { return strings.Compare(a.Key+"\x00"+a.Value, b.Key+"\x00"+b.Value) })
		ir := leakcheck.IndexRow{ID: r.ID.String(), Table: t.String(), Label: label, Source: string(src), Texts: texts, Tokens: tokens}
		if t == schema.TableMetaKeys && fieldBool(r, leakKeyInternal) {
			ir.KeyNames = []string{fieldText(r, leakKeyName)}
		}
		out = append(out, ir)
	}
	return out
}
