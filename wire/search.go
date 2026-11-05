package wire

// search.go — the core half of `lore search` (ops/search.Env), ported from
// the old binary's search_global.go (PASS3 ONE-PATH, P3-MISC-1).
//
//	per target table:
//	  key:value on a visible meta key   ─▶ row_meta conditions (METADATA 3.17)
//	  field:value on a resource field   ─▶ resources only (RES 3.12)
//	  otherwise                         ─▶ FTS5 over every store's cache (retry quoted on a syntax error)
//	─▶ scope filters (repo rows: project / repo / archived; store rows: archived)
//	─▶ RRF merge 1/(60+rank), cut to --limit ─▶ trash tail (--include-deleted)
//
// Rows outside this checkout are the attached stores' rows (r2-source):
// each such hit carries its FLAG1 `_source`. v0.1.17's second listing of
// store clones from its own cache is gone with lore.db (P3-MISC-1).

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/metaquery"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	"github.com/thesatellite-ai/lore/store"
)

var _ opssearch.Env = (*App)(nil)

// Search tuning.
const (
	// searchFetch is how many cache hits one table may return before the
	// scope filters (v0.1.17 filtered in SQL before its LIMIT; the cache
	// cannot, so it over-fetches and cuts after filtering).
	searchFetch = 1000
	// searchMask prints in place of a sensitive value a term matched on
	// (OV34/OV39-SENSCOND; same mark as logic/meta.Show).
	searchMask = "•••"
)

// Row fields the scope filters read.
const (
	searchFieldProject  = "project_id"
	searchFieldRepo     = "repo_id"
	searchFieldArchived = "archived_at"
)

// searchScope is the resolved scope of one search.
type searchScope struct {
	projectID       string
	repoID          string
	allRepos        bool
	masterOnly      bool
	includeArchived bool
}

// keeps reports whether repo row r passes t's v0.1.17 filters under sc.
func (sc searchScope) keeps(t opssearch.Target, r row.Row) bool {
	if r.Deleted {
		return false
	}
	if t.HasProject && sc.projectID != "" {
		if p := searchField(r, searchFieldProject); p != "" && p != sc.projectID {
			return false
		}
	}
	if t.HasRepo && !sc.allRepos {
		repo := searchField(r, searchFieldRepo)
		switch {
		case sc.masterOnly:
			if repo != "" {
				return false
			}
		case sc.repoID != "":
			if repo != "" && repo != sc.repoID {
				return false
			}
		}
	}
	return !t.HasArchived || sc.includeArchived || searchField(r, searchFieldArchived) == ""
}

// keepsStoreRow is keeps for a row of an attached store: the repo's
// project/repo scope does not apply to another store's rows; the deleted
// and archived filters do.
func (sc searchScope) keepsStoreRow(t opssearch.Target, r row.Row) bool {
	if r.Deleted {
		return false
	}
	return !t.HasArchived || sc.includeArchived || searchField(r, searchFieldArchived) == ""
}

// searchField is the text form of a row field ("" when absent or null).
func searchField(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// GlobalSearch implements opssearch.Env.
func (a *App) GlobalSearch(ctx context.Context, r opssearch.Request) (opssearch.Result, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return opssearch.Result{}, err
	}
	sc, err := a.searchScope(ctx, r)
	if err != nil {
		return opssearch.Result{}, err
	}
	// METADATA 3.17: key:value / key=value / key? on a visible meta key are
	// exact conditions; then field:value terms naming a resource field
	// (RES 3.12); the rest is free text.
	mconds, rest, msens, err := a.metaSearchTerms(ctx, r.Query)
	if err != nil {
		return opssearch.Result{}, err
	}
	var conds []resourceCond
	text := rest
	if len(mconds) == 0 {
		// OV39-META-FIRST: with meta conditions the resource field path is
		// not taken (a name that is both is a meta key here)
		if conds, text, err = a.resourceFieldTerms(ctx, rest); err != nil {
			return opssearch.Result{}, err
		}
	}
	ftsText := metaquery.FTS(metaquery.Parse(text, func(string) bool { return false }))

	var res opssearch.Result
	for _, t := range r.Targets {
		var hits []opssearch.Hit
		switch {
		case len(mconds) > 0:
			hits, err = a.searchMetaTable(ctx, eng, t, mconds, msens, ftsText, sc, r.PerTable)
		case len(conds) > 0 && t.Entity == opssearch.EntityResource:
			hits, err = a.searchResourceFields(ctx, eng, t, conds, text, sc, r.PerTable)
		case len(conds) > 0 && text == "":
			continue // only field conditions: nothing for other tables
		default:
			hits, err = a.searchCoreTable(ctx, eng, t, ftsText, sc, r.PerTable)
		}
		if err != nil {
			return opssearch.Result{}, err
		}
		if len(hits) == 0 {
			continue
		}
		res.MatchedTables++
		// RRF: per-table rank 0 is best; score = 1 / (k + rank).
		for rank, h := range hits {
			h.Score = 1.0 / (opssearch.RRFK + float64(rank))
			res.Hits = append(res.Hits, h)
		}
	}
	// Stable: equal scores keep table order (deterministic output).
	sort.SliceStable(res.Hits, func(i, j int) bool { return res.Hits[i].Score > res.Hits[j].Score })
	if len(res.Hits) > r.Limit {
		res.Hits = res.Hits[:r.Limit]
	}
	if r.IncludeDeleted {
		// DELETION 2.9 (FA-META-4): the cache holds no trashed row, so rows
		// in the trash are matched as plain text and listed after the live
		// hits, marked.
		trashed, terr := a.searchTrashed(ctx, eng, r.Targets, plainSearchText(rest), sc, r.Limit)
		if terr != nil {
			return opssearch.Result{}, terr
		}
		res.Hits = append(res.Hits, trashed...)
	}
	return res, nil
}

// searchScope resolves the project (the repo's one project) and --repo
// (a rep_ id or mount name).
func (a *App) searchScope(ctx context.Context, r opssearch.Request) (searchScope, error) {
	sc := searchScope{allRepos: r.AllRepos, masterOnly: r.MasterOnly, includeArchived: r.IncludeArchived}
	pid, err := a.Project(ctx)
	switch {
	case err == nil:
		sc.projectID = pid.String()
	case errs.Has(err, errs.NotFound):
		// a store-only checkout has no project: no project filter
	default:
		return searchScope{}, err
	}
	if r.Repo != "" {
		rid, err := a.RepoID(ctx, r.Repo)
		if err != nil {
			return searchScope{}, errs.New(errs.NotFound, "no repo with mount_name "+quoteSearch(r.Repo)+" in current project").
				WithHint("lore repo list")
		}
		sc.repoID = rid.String()
	}
	return sc, nil
}

// quoteSearch is s in double quotes.
func quoteSearch(s string) string { return `"` + s + `"` }

// searchCoreTable runs query over t in every store's cache and returns t's
// in-scope hits, best first, at most perTable. A query FTS5 rejects as
// syntax is retried once with every word quoted (v0.1.17's fallback); a
// second refusal is E_INVALID_INPUT.
func (a *App) searchCoreTable(ctx context.Context, eng *engine.Engine, t opssearch.Target, query string, sc searchScope, perTable int) ([]opssearch.Hit, error) {
	hits, err := a.Search(ctx, t.Table, query, searchFetch)
	if errs.Has(err, errs.InvalidInput) {
		if quoted := quoteSearchTerms(query); quoted != "" && quoted != query {
			hits, err = a.Search(ctx, t.Table, quoted, searchFetch)
		}
	}
	if err != nil {
		if errs.Has(err, errs.InvalidInput) {
			return nil, errs.Wrap(errs.InvalidInput, "fts5 query against "+t.Entity, err).
				WithHint("FTS5 syntax: 'foo*' / '\"phrase\"' / 'a OR b' / 'a NOT b'")
		}
		return nil, err
	}
	rows := map[store.Source]map[ids.AnyID]row.Row{} // per store, loaded on its first hit
	out := make([]opssearch.Hit, 0, perTable)
	for _, h := range hits {
		if len(out) == perTable {
			break
		}
		byID, ok := rows[h.Source]
		if !ok {
			list, err := eng.List(ctx, h.Source, t.Table)
			if err != nil {
				return nil, err
			}
			byID = make(map[ids.AnyID]row.Row, len(list))
			for _, r := range list {
				byID[r.ID] = r
			}
			rows[h.Source] = byID
		}
		r, ok := byID[h.ID]
		if !ok {
			continue // gone since the cache was refreshed
		}
		if h.Source == store.SourceRepo {
			if sc.keeps(t, r) {
				out = append(out, hitOf(t, h.ID.String(), h.Snippet, r))
			}
			continue
		}
		// r2-source: a row of an attached store is a hit too, labelled with
		// its FLAG1 _source.
		if !sc.keepsStoreRow(t, r) {
			continue
		}
		hit := hitOf(t, h.ID.String(), h.Snippet, r)
		hit.Source = a.DescribeSource(h.Source)
		out = append(out, hit)
	}
	return out, nil
}

// searchTrashed matches the rows in the trash of every attached store
// against text (every word, case-insensitive, in the table's full-text
// fields; resources left out, their values may be sensitive) under the
// same scope filters, at most limit, each marked InTrash (FA-META-4).
func (a *App) searchTrashed(ctx context.Context, eng *engine.Engine, targets []opssearch.Target, text string, sc searchScope, limit int) ([]opssearch.Hit, error) {
	words := strings.Fields(strings.ToLower(text))
	if len(words) == 0 {
		return nil, nil
	}
	byTable := map[row.Table]opssearch.Target{}
	for _, t := range targets {
		if t.Entity != opssearch.EntityResource {
			byTable[t.Table] = t
		}
	}
	all, err := a.Trashed(ctx)
	if err != nil {
		return nil, err
	}
	var out []opssearch.Hit
	for _, tr := range all {
		if len(out) == limit {
			break
		}
		t, ok := byTable[tr.Row.Table]
		if !ok {
			continue
		}
		def, ok := eng.Registry().Table(tr.Row.Table)
		if !ok {
			continue
		}
		live := tr.Row
		live.Deleted = false // the scope filters, minus the deleted test
		if tr.Store == store.SourceRepo && !sc.keeps(t, live) || tr.Store != store.SourceRepo && !sc.keepsStoreRow(t, live) {
			continue
		}
		var hay strings.Builder
		for _, f := range def.FTS {
			hay.WriteString(strings.ToLower(searchField(live, f)) + "\n")
		}
		if !allWords(hay.String(), words) {
			continue
		}
		hit := hitOf(t, live.ID.String(), "", live)
		hit.InTrash = true
		if tr.Store != store.SourceRepo {
			hit.Source = a.DescribeSource(tr.Store)
		}
		out = append(out, hit)
	}
	return out, nil
}

// allWords reports whether every word occurs in hay.
func allWords(hay string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// hitOf is a hit labelled like v0.1.17's hydrateGlobalHits: pretty = the
// id, Body = the table's label field (body-labelled ones cut).
func hitOf(t opssearch.Target, id, snippet string, r row.Row) opssearch.Hit {
	h := opssearch.Hit{EntityTable: t.Table.String(), Entity: t.Entity, ID: id, Pretty: id, Snippet: snippet}
	if t.Label.Field != "" {
		h.Body = searchField(r, t.Label.Field)
		if t.Label.Trunc {
			h.Body = opssearch.TruncBody(h.Body, opssearch.BodyMax)
		}
	}
	h.Fallback = opssearch.TruncBody(strings.Join(strings.Fields(searchField(r, "body")), " "), opssearch.BodyMax)
	return h
}

// quoteSearchTerms rewrites free text as an FTS5 query matching every word
// literally (each word a quoted phrase, embedded quotes doubled).
func quoteSearchTerms(q string) string {
	fields := strings.Fields(q)
	for i, f := range fields {
		fields[i] = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
	}
	return strings.Join(fields, " ")
}

// plainSearchText turns an FTS5 query into plain words: operators, quotes
// and wildcards dropped.
func plainSearchText(q string) string {
	r := strings.NewReplacer(`"`, " ", "*", " ", "(", " ", ")", " ")
	var words []string
	for _, w := range strings.Fields(r.Replace(q)) {
		switch w {
		case "AND", "OR", "NOT", "NEAR":
			continue
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// resourceCond is one field:value term: a resource field name and the
// value it must equal (case-insensitive). sensitive is set when any kind
// marks the field sensitive (fail closed): the term matches the real
// value, which is never printed (OV34/OV39-SENSCOND).
type resourceCond struct {
	field, value string
	sensitive    bool
}

// saidValue is "name: value" for a hit's label, masked when sensitive.
func saidValue(name, v string, sensitive bool) string {
	if sensitive {
		v = searchMask
	}
	return name + ": " + v
}

// resourceFieldTerms splits query (RES 3.12) into conditions on resource
// field names known to this repo's lens and the remaining free text.
func (a *App) resourceFieldTerms(ctx context.Context, query string) ([]resourceCond, string, error) {
	rest := strings.Fields(query)
	if !slices.ContainsFunc(rest, func(w string) bool { i := strings.IndexAny(w, ":="); return i > 0 && i < len(w)-1 }) {
		return nil, query, nil
	}
	known, err := a.resourceFieldNames(ctx)
	if err != nil {
		return nil, "", err
	}
	var conds []resourceCond
	text := rest[:0:0]
	for _, w := range rest {
		i := strings.IndexAny(w, ":=")
		if sens, ok := known[w[:max(i, 0)]]; i > 0 && i < len(w)-1 && ok {
			conds = append(conds, resourceCond{field: w[:i], value: strings.Trim(w[i+1:], `"'`), sensitive: sens})
			continue
		}
		text = append(text, w)
	}
	return conds, strings.Join(text, " "), nil
}

// resourceFieldNames is every kind field name in this repo's lens, mapped
// to whether any kind marks it sensitive.
func (a *App) resourceFieldNames(ctx context.Context) (map[string]bool, error) {
	res, err := a.SQL(ctx, opssql.Request{Query: `SELECT name, MAX(COALESCE(sensitive, 0)) FROM kind_fields_all GROUP BY name`, Timeout: opssql.DefaultTimeout})
	if err != nil {
		if errs.Has(err, errs.InvalidInput) {
			return map[string]bool{}, nil // no kinds table in this cache yet
		}
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range res.Rows {
		if len(r) > 1 {
			out[r[0].String()] = r[1].String() != "0" // MAX(COALESCE(sensitive, 0)): 0 or 1
		}
	}
	return out, nil
}

// sqlText is s as a SQL string literal.
func sqlText(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// ftsOnly is the ids of t's full-text hits of text, or nil (no narrowing)
// when text is empty.
func (a *App) ftsOnly(ctx context.Context, t opssearch.Target, text string) (map[string]bool, error) {
	if text == "" {
		return nil, nil
	}
	ft, err := a.Search(ctx, t.Table, text, searchFetch)
	if err != nil {
		return nil, err
	}
	only := make(map[string]bool, len(ft))
	for _, h := range ft {
		only[h.ID.String()] = true
	}
	return only, nil
}

// repoRows is t's repo-store rows by id.
func repoRows(ctx context.Context, eng *engine.Engine, t opssearch.Target) (map[string]row.Row, error) {
	list, err := eng.List(ctx, store.SourceRepo, t.Table)
	if err != nil {
		return nil, err
	}
	rows := make(map[string]row.Row, len(list))
	for _, r := range list {
		rows[r.ID.String()] = r
	}
	return rows, nil
}

// searchResourceFields is the resources entry when the query has field
// conditions: this repo's resources whose fields equal every condition,
// narrowed to the full-text hits of text when not empty.
func (a *App) searchResourceFields(ctx context.Context, eng *engine.Engine, t opssearch.Target, conds []resourceCond, text string, sc searchScope, perTable int) ([]opssearch.Hit, error) {
	q := `SELECT r.id FROM resources_all r WHERE r.source = ` + sqlText(string(store.SourceRepo))
	var said []string
	for _, c := range conds {
		q += ` AND EXISTS (SELECT 1 FROM kind_fields_all f WHERE f.kind_id = r.kind AND f.name = ` + sqlText(c.field) +
			` AND lower(CAST(json_extract(r.fields, '$."' || f.id || '".v') AS TEXT)) = lower(` + sqlText(c.value) + `))`
		said = append(said, saidValue(c.field, c.value, c.sensitive))
	}
	q += ` ORDER BY r.name`
	// OV34/OV39-SENSCOND: match on the real value (unmasked lens); only the
	// id comes back and the label masks a sensitive term's value.
	res, err := a.SQL(ctx, opssql.Request{Query: q, ShowSensitive: true, Timeout: opssql.DefaultTimeout})
	if err != nil {
		return nil, err
	}
	only, err := a.ftsOnly(ctx, t, quoteSearchTermsOrEmpty(text))
	if err != nil {
		return nil, err
	}
	rows, err := repoRows(ctx, eng, t)
	if err != nil {
		return nil, err
	}
	out := make([]opssearch.Hit, 0, perTable)
	for _, cells := range res.Rows {
		if len(out) == perTable || len(cells) == 0 {
			break
		}
		id := cells[0].String()
		r, ok := rows[id]
		if !ok || !sc.keeps(t, r) || (only != nil && !only[id]) {
			continue
		}
		h := hitOf(t, id, "", r)
		h.Body = searchField(r, "name") + "  (field " + strings.Join(said, ", ") + ")"
		out = append(out, h)
	}
	return out, nil
}

// quoteSearchTermsOrEmpty is quoteSearchTerms, "" for blank text.
func quoteSearchTermsOrEmpty(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return quoteSearchTerms(text)
}

// metaSearchTerms splits query into metadata conditions (METADATA 3.17:
// terms whose key is a metadata key name this lens can see) and the other
// words, plus the set of keys marked sensitive anywhere. A query without
// any condition character is not looked at (no lens query).
func (a *App) metaSearchTerms(ctx context.Context, query string) ([]metaquery.Term, string, map[string]bool, error) {
	if !strings.ContainsAny(query, ":=?<>~!") { // OV39-CMP: comparisons too
		return nil, query, nil, nil
	}
	known := map[string]bool{}
	sensitive := map[string]bool{}
	for _, q := range []string{
		`SELECT key_name, MAX(COALESCE(sensitive, 0)) FROM row_meta_all WHERE item_idx = -1 GROUP BY key_name`,
		`SELECT name, MAX(COALESCE(sensitive, 0)) FROM meta_keys_all GROUP BY name`,
		// METADATA 3.20: by key id too, so an old label of a
		// registry-sensitive key is sensitive as well
		`SELECT r.key_name, 1 FROM row_meta_all r JOIN meta_keys_all k ON k.id = r.key_id WHERE r.item_idx = -1 AND COALESCE(k.sensitive, 0) <> 0 GROUP BY r.key_name`,
	} {
		res, err := a.SQL(ctx, opssql.Request{Query: q, Timeout: opssql.DefaultTimeout})
		if err != nil {
			if errs.Has(err, errs.InvalidInput) {
				continue // no such view in this cache yet
			}
			return nil, "", nil, err
		}
		for _, r := range res.Rows {
			if len(r) > 1 {
				known[r[0].String()] = true
				if r[1].String() != "0" { // MAX(COALESCE(sensitive, 0)): 0 or 1
					sensitive[r[0].String()] = true
				}
			}
		}
	}
	var conds []metaquery.Term
	var words []string
	for _, t := range metaquery.Parse(query, func(n string) bool { return known[n] }) {
		if t.Kind == metaquery.Text {
			words = append(words, t.Word)
			continue
		}
		conds = append(conds, t)
	}
	return conds, strings.Join(words, " "), sensitive, nil
}

// searchKeySensitive tells whether a meta term's name (key or key.path) is
// sensitive by itself or by its top-level key (fail closed).
func searchKeySensitive(name string, sensitive map[string]bool) bool {
	if sensitive[name] {
		return true
	}
	top, _, ok := strings.Cut(name, ".")
	return ok && sensitive[top]
}

// searchMetaTable is one table's entry when the query has metadata
// conditions: this repo's rows matching every condition through row_meta,
// narrowed to the full-text hits of ftsText when not empty.
func (a *App) searchMetaTable(ctx context.Context, eng *engine.Engine, t opssearch.Target, conds []metaquery.Term, sensitive map[string]bool, ftsText string, sc searchScope, perTable int) ([]opssearch.Hit, error) {
	q := `SELECT DISTINCT r.entity_id FROM row_meta_all r WHERE r.source = ` + sqlText(string(store.SourceRepo)) +
		` AND r.entity_table = ` + sqlText(t.Table.String()) + ` AND r.item_idx = -1`
	for _, c := range conds {
		// P2-META-9: repo:<name>:… names a repo; search reads only this
		// repo's rows, so another repo's name matches nothing here.
		if c.Level == metaquery.LevelRepo && c.Place != "" {
			if dir, derr := a.RepoDirName(ctx); derr != nil || dir != c.Place {
				return nil, nil
			}
		}
	}
	var said []string
	for _, c := range conds {
		cc := c
		exists := "EXISTS"
		if c.Kind == metaquery.Cmp && c.Op == meta.OpNe {
			// != : the row has the key and no leaf equals the value
			cc.Kind, exists = metaquery.Cond, "NOT EXISTS"
			q += ` AND EXISTS (SELECT 1 FROM row_meta_all h WHERE h.source = r.source AND h.entity_table = r.entity_table` +
				` AND h.entity_id = r.entity_id AND h.key_name = ` + sqlText(c.Name()) + ` AND h.item_idx = -1)`
		}
		match, err := sqlite.RowMetaCond("x", cc)
		if err != nil {
			return nil, err
		}
		switch c.Kind {
		case metaquery.Has:
			said = append(said, c.Name()+"?")
		case metaquery.Cmp:
			v := c.Value
			if searchKeySensitive(c.Name(), sensitive) {
				v = searchMask
			}
			said = append(said, c.Name()+" "+string(c.Op)+" "+v)
		default:
			said = append(said, saidValue(c.Name(), c.Value, searchKeySensitive(c.Name(), sensitive)))
		}
		q += ` AND ` + exists + ` (SELECT 1 FROM row_meta_all x WHERE x.source = r.source AND x.entity_table = r.entity_table` +
			` AND x.entity_id = r.entity_id AND x.key_name = ` + sqlText(c.Name()) + ` AND ` + match + `)`
	}
	q += ` ORDER BY r.entity_id`
	res, err := a.SQL(ctx, opssql.Request{Query: q, ShowSensitive: true, Timeout: opssql.DefaultTimeout})
	if err != nil {
		if errs.Has(err, errs.InvalidInput) {
			return nil, nil // no row_meta view in this cache yet
		}
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, nil
	}
	only, err := a.ftsOnly(ctx, t, ftsText)
	if err != nil {
		return nil, err
	}
	rows, err := repoRows(ctx, eng, t)
	if err != nil {
		return nil, err
	}
	out := make([]opssearch.Hit, 0, perTable)
	for _, cells := range res.Rows {
		if len(out) == perTable || len(cells) == 0 {
			break
		}
		id := cells[0].String()
		r, ok := rows[id]
		if !ok || !sc.keeps(t, r) || (only != nil && !only[id]) {
			continue
		}
		h := hitOf(t, id, "", r)
		label := h.Body
		if label == "" {
			label = h.Fallback
		}
		h.Body = strings.TrimSpace(label + "  (" + strings.Join(said, ", ") + ")")
		out = append(out, h)
	}
	return out, nil
}

// SearchStatus implements opssearch.Env: per target table, the rows and
// the full-text rows summed over every store's cache.
func (a *App) SearchStatus(ctx context.Context, targets []opssearch.Target) ([]opssearch.TableStatus, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	st.caches.mu.Lock()
	order := append([]store.Source(nil), st.caches.order...)
	st.caches.mu.Unlock()
	out := make([]opssearch.TableStatus, 0, len(targets))
	for _, t := range targets {
		ts := opssearch.TableStatus{Entity: t.Entity, Table: t.Table.String()}
		for _, s := range order {
			c, err := st.caches.of(s)
			if err != nil {
				return nil, err
			}
			n, err := c.FTSCounts(ctx, s, t.Table)
			if errs.Has(err, errs.NotFound) {
				continue // a cache without this table (kinds only)
			}
			if err != nil {
				return nil, err
			}
			ts.FTSTable = n.FTSTable
			ts.SourceRows += n.Rows
			if n.HasFTS {
				ts.FTSRows += n.Indexed
			} else {
				ts.FTSRows += n.Rows // nothing to index: never drift
			}
		}
		out = append(out, ts)
	}
	return out, nil
}

// RebuildSearch implements opssearch.Env: every attached store's cache is
// emptied (Cache.Rebuild clears its rows, refs and cursor) and refilled
// from its row files (Engine.Refresh from the zero cursor). Rows are never
// touched: the files are the truth (STORE_SERVER_SPEC 3).
func (a *App) RebuildSearch(ctx context.Context) ([]string, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	st.caches.mu.Lock()
	order := append([]store.Source(nil), st.caches.order...)
	st.caches.mu.Unlock()
	names := make([]string, 0, len(order))
	for _, s := range order {
		if err := st.caches.Rebuild(ctx, s); err != nil {
			return nil, err
		}
		if _, err := st.eng.Refresh(ctx, s); err != nil {
			return nil, err
		}
		names = append(names, string(s))
	}
	return names, nil
}
