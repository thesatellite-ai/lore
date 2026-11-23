package trash

// born.go — `lore trash add --born-branch <b> [--kind <k>] [--dry-run]`
// (D-392): every note created on a code branch, found by its provenance
// (`_o.git.branch`, written once at create), moved to the trash as one
// batch. A land, an adopt or a revert copies a row whole, so the rows are
// found on the branch and after they landed on main.
//
//	read stores ─ List(note table) ─ engine.BornOn ─ --kind
//	   └ counts per kind ─ --dry-run: print, write nothing
//	   └ bulk.Ask (always, a bulk delete: E_NEEDS_CONFIRM without --yes)
//	   └ bulk.Begin(delete) ─ DeleteMany ─ one bat_ id
//	        └ lore trash restore --id <row> | --id <bat_> brings them back
//
// Only note tables are swept (noteTables): the org's own rows (actors,
// repos, projects, policies, tags, metadata keys, secrets settings,
// teams, machines, profiles) are setup, never "what the branch wrote",
// and trashing one could take an org apart.

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// OpAdd is `lore trash add`: move rows to the trash by a filter.
const OpAdd = "trash.add"

// noteTables are the tables `trash add --born-branch` sweeps, in the
// order the counts print: the rows people and agents write while they
// work (knowledge, coordination, content), never the org's setup.
var noteTables = []row.Table{
	schema.TableTasks, schema.TableTasklists, schema.TableMissions, schema.TablePlans,
	schema.TableMemories, schema.TableRules, schema.TableDecisions, schema.TableHotfixes,
	schema.TablePatterns, schema.TablePlaybooks, schema.TablePrompts,
	schema.TableHandoffs, schema.TableReminders, schema.TableComments, schema.TableCommitLinks,
	schema.TableKnowledgeRefs, schema.TableRuleVerifierRefs,
	schema.TableArchitectureNotes, schema.TableBehaviours, schema.TableCookbookRecipes,
	schema.TableIncidents, schema.TableSuggestions, schema.TableTastePrefs, schema.TableWorkflows,
	schema.TableSnapshots, schema.TableTechDocs, schema.TableExternalSources,
	schema.TableTaskTemplates, schema.TableTaskViews,
}

// NoteTables returns the tables `trash add --born-branch` sweeps (and
// `lore branch show` counts), so both answer for the same rows.
func NoteTables() []row.Table { return slices.Clone(noteTables) }

// AddIn is the input of trash.add.
type AddIn struct {
	BornBranch string   `json:"born_branch" flag:"born-branch" desc:"required: move every note created on this code branch (its origin's git branch), also after the branch landed"`
	Kind       []string `json:"kind,omitempty" flag:"kind" desc:"only rows of this kind: a table or its singular (task, memory, rule, …; repeatable)"`
	DryRun     bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"print the count per kind and the rows; write nothing"`
}

// AddOut is what trash.add moved (or, with --dry-run, would move).
type AddOut struct {
	BornBranch string  `json:"born_branch"`
	Kinds      []Count `json:"kinds"`
	Rows       []Item  `json:"rows"`
	Count      int     `json:"count"`
	Batch      string  `json:"batch,omitempty"`
	DryRun     bool    `json:"dry_run,omitempty"`
	// Dependents counts the rows the delete takes with the matched ones
	// (the sub-tasks of a task, the tasks of a task list), all born on
	// the branch too.
	Dependents int `json:"dependents,omitempty"`
	// Kept are rows born on the branch that stay: their delete would take
	// rows created elsewhere with them (a task list made on the branch
	// that main's tasks now use), or the delete refuses them.
	Kept []Item `json:"kept,omitempty"`
}

// Previewer is the optional upgrade that plans a delete without writing
// (engine.PreviewDelete; *wire.App has it). With it trash.add keeps a
// row whose cascade would reach rows born elsewhere.
type Previewer interface {
	PreviewDelete(ctx context.Context, refs ...engine.Ref) (engine.DeletePreview, error)
}

// BornEnv is what trash.add needs beyond Env: the read stores, their
// rows and the one-batch delete (*wire.App has all three). Without it
// the op answers E_UNSUPPORTED.
type BornEnv interface {
	Stores() []store.Source
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	DeleteMany(ctx context.Context, refs []engine.Ref) error
}

// bornHit is one matched row and the store holding it.
type bornHit struct {
	src store.Source
	r   row.Row
}

// add is trash.add.
func (o ops) add(ctx context.Context, in AddIn) (AddOut, error) {
	branch, err := engine.ParseBornBranch(in.BornBranch)
	if err != nil {
		return AddOut{}, err
	}
	if branch == "" {
		return AddOut{}, errs.New(errs.InvalidInput, "trash add: --born-branch is required").
			WithHint("lore trash add --born-branch feat/x --dry-run")
	}
	tables, err := kindTables(in.Kind)
	if err != nil {
		return AddOut{}, err
	}
	env, ok := upgrade.As[BornEnv](o.env)
	if !ok {
		return AddOut{}, errs.New(errs.Unsupported, "trash add: this host cannot list and delete rows")
	}
	hits, err := bornRows(ctx, env, branch, tables)
	if err != nil {
		return AddOut{}, err
	}
	out := AddOut{BornBranch: branch, Kinds: []Count{}, Rows: []Item{}, DryRun: in.DryRun}
	if pv, ok := upgrade.As[Previewer](o.env); ok {
		if hits, out.Kept, out.Dependents, err = keepShared(ctx, pv, branch, hits); err != nil {
			return AddOut{}, err
		}
	}
	per := map[string]int{}
	for _, h := range hits {
		per[h.r.Table.String()]++
		out.Rows = append(out.Rows, Item{ID: h.r.ID.String(), Table: h.r.Table.String(), Store: string(h.src), Title: titleOf(h.r)})
	}
	for _, t := range tables {
		if n := per[t.String()]; n > 0 {
			out.Kinds = append(out.Kinds, Count{Name: t.String(), Count: n})
		}
	}
	out.Count = len(hits)
	if len(hits) == 0 {
		return out, nil
	}
	refs := make([]engine.Ref, 0, len(hits))
	for _, h := range hits {
		refs = append(refs, engine.Ref{Store: h.src, Table: h.r.Table, ID: h.r.ID})
	}
	if in.DryRun {
		// devco:D-932: the dry run runs the real DeleteMany check-only
		// (Run marks a preview engine.WithCheckOnly), confirmed as the
		// write is once bulk.Ask has its yes, so a row the write refuses
		// (role, policy, a stale or purged row) fails here with the same
		// code and nothing goes to the trash.
		if err := env.DeleteMany(engine.WithConfirmed(engine.WithCheckOnly(ctx)), refs); err != nil {
			return AddOut{}, errs.Prefix("trash add --born-branch "+branch+" --dry-run: would fail (nothing written)", err)
		}
		return out, nil
	}
	// The question names the counts per kind and ids only, never a title
	// (G-ENGINE-5: E_NEEDS_CONFIRM travels in logs and REST bodies).
	if err := bulk.Ask(ctx, "move "+kindsText(out.Kinds)+" created on "+branch+" to the trash?"); err != nil {
		return AddOut{}, err
	}
	bctx, batch, err := bulk.Begin(ctx, bulk.KindDelete)
	if err != nil {
		return AddOut{}, err
	}
	out.Batch = batch.String()
	if err := env.DeleteMany(bctx, refs); err != nil {
		return AddOut{}, errs.Prefix("trash add --born-branch "+branch+": the "+strconv.Itoa(len(refs))+" rows could not go to the trash (batch "+out.Batch+"; `lore trash restore --id "+out.Batch+"` brings back any that did)", err)
	}
	return out, nil
}

// keepShared splits hits into the rows to move and the rows to keep: a
// row whose delete would take a row not born on branch (a task list made
// on the branch that later held main's tasks), or that the delete itself
// refuses, is kept and named. It also counts the dependents the moved
// rows take with them.
func keepShared(ctx context.Context, pv Previewer, branch string, hits []bornHit) ([]bornHit, []Item, int, error) {
	var move []bornHit
	var kept []Item
	for _, h := range hits {
		it := Item{ID: h.r.ID.String(), Table: h.r.Table.String(), Store: string(h.src), Title: titleOf(h.r)}
		p, err := pv.PreviewDelete(ctx, engine.Ref{Store: h.src, Table: h.r.Table, ID: h.r.ID})
		if err != nil {
			it.Reason = string(errs.CodeOf(err)) + ": the delete refuses it"
			kept = append(kept, it)
			continue
		}
		var other string
		for _, c := range p.Rows {
			if !engine.BornOn(c.Row, branch) {
				other = c.Row.ID.String()
				break
			}
		}
		if other != "" {
			it.Reason = "its delete would take " + other + ", created elsewhere"
			kept = append(kept, it)
			continue
		}
		move = append(move, h)
	}
	if len(move) == 0 {
		return move, kept, 0, nil
	}
	refs := make([]engine.Ref, len(move))
	for i, h := range move {
		refs[i] = engine.Ref{Store: h.src, Table: h.r.Table, ID: h.r.ID}
	}
	p, err := pv.PreviewDelete(ctx, refs...)
	if err != nil {
		return nil, nil, 0, err
	}
	return move, kept, p.Dependents(len(refs)), nil
}

// bornRows is every live row of tables, in every read store, created on
// branch: store, then table order, then id.
func bornRows(ctx context.Context, env BornEnv, branch string, tables []row.Table) ([]bornHit, error) {
	var hits []bornHit
	seen := map[string]bool{} // a row read through two read stores counts once
	for _, src := range engine.ReadStoresOf(env) {
		for _, t := range tables {
			rows, err := env.List(ctx, src, t)
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				if r.Deleted || !engine.BornOn(r, branch) || seen[r.ID.String()] {
					continue
				}
				seen[r.ID.String()] = true
				hits = append(hits, bornHit{src: src, r: r})
			}
		}
	}
	return hits, nil
}

// BornCounts counts the live note rows created on branch in env's read
// stores, per table (`lore branch show`: how many of the branch's rows
// are on the shared notes, landed or adopted).
func BornCounts(ctx context.Context, env BornEnv, branch string) ([]Count, error) {
	hits, err := bornRows(ctx, env, branch, noteTables)
	if err != nil {
		return nil, err
	}
	per := map[row.Table]int{}
	for _, h := range hits {
		per[h.r.Table]++
	}
	var out []Count
	for _, t := range noteTables {
		if n := per[t]; n > 0 {
			out = append(out, Count{Name: t.String(), Count: n})
		}
	}
	return out, nil
}

// kindTables resolves --kind (a table or its singular, as trash list
// --type) to note tables; none given is every note table. A kind that
// names no note table is E_INVALID_INPUT listing the kinds.
func kindTables(kinds []string) ([]row.Table, error) {
	if len(kinds) == 0 {
		return slices.Clone(noteTables), nil
	}
	var out []row.Table
	for _, k := range kinds {
		var hit row.Table
		for _, t := range noteTables {
			if typeMatches(t.String(), strings.ReplaceAll(k, "-", "_")) {
				hit = t
				break
			}
		}
		if hit == "" {
			names := make([]string, len(noteTables))
			for i, t := range noteTables {
				names[i] = t.String()
			}
			return nil, errs.New(errs.InvalidInput, "trash add: --kind "+strconv.Quote(k)+" is not a kind of note").
				WithHint("one of: " + strings.Join(names, ", "))
		}
		if !slices.Contains(out, hit) {
			out = append(out, hit)
		}
	}
	return out, nil
}

// kindsText is "2 tasks, 1 memory, 1 task list": each count with its
// table as a word, singular for one row (the wording of lore branch).
func kindsText(cs []Count) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		word := strings.ReplaceAll(c.Name, "_", " ")
		if c.Count == 1 {
			if stem, ok := strings.CutSuffix(word, "ies"); ok {
				word = stem + "y"
			} else if stem, ok := strings.CutSuffix(word, "es"); ok && (strings.HasSuffix(stem, "x") || strings.HasSuffix(stem, "ch")) {
				word = stem
			} else {
				word = strings.TrimSuffix(word, "s")
			}
		}
		parts[i] = strconv.Itoa(c.Count) + " " + word
	}
	return strings.Join(parts, ", ")
}

// Text implements op.Texter: the count per kind first, then the rows,
// then what to run next.
func (o AddOut) Text() string {
	var b strings.Builder
	if o.Count == 0 {
		b.WriteString("no notes created on " + o.BornBranch + "; nothing moved\n")
		b.WriteString(keptText(o.Kept))
		return b.String()
	}
	verb := "moved"
	if o.DryRun {
		verb = "would move"
	}
	b.WriteString(verb + " " + count(o.Count, "row") + " created on " + o.BornBranch + " to the trash: " + kindsText(o.Kinds))
	if o.Batch != "" {
		b.WriteString(" (batch " + o.Batch + ")")
	}
	if o.Dependents > 0 {
		b.WriteString(", with " + count(o.Dependents, "row") + " that go with them")
	}
	b.WriteString("\n")
	for _, it := range o.Rows {
		line := "  " + it.ID + " (" + it.Table + ", " + it.Store + ")"
		if it.Title != "" {
			line += "  " + it.Title
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(keptText(o.Kept))
	switch {
	case o.DryRun:
		b.WriteString("dry run: nothing written; rerun without --dry-run and with --yes to move them\n")
	case o.Batch != "":
		b.WriteString("bring them back: lore trash restore --id " + o.Batch + " (all) or lore trash restore --id <row-id> (one)\n")
	}
	return b.String()
}

// keptText names the rows born on the branch that stay, and why.
func keptText(kept []Item) string {
	if len(kept) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("kept " + count(len(kept), "row") + " created on the branch:\n")
	for _, it := range kept {
		b.WriteString("  " + it.ID + " (" + it.Table + ", " + it.Store + ")  " + it.Reason + "\n")
	}
	return b.String()
}
