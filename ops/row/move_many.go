package row

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/wheredel"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Bulk move (devco D-712, owner 2026-11-19: a folder of 296 notes took 296
// `lore move --id` runs and ~600 commits):
//
//	lore move --from <store>[/<folder>] --to <place> [--where …] [--dry-run]
//	  ─► every live row of the movable tables in --from matching --where
//	  ─► --dry-run: the list, nothing written, nothing asked
//	  ─► the company-row admin check (the one-row move's), then the
//	     question naming the rows
//	  ─► engine.MoveMany: each row through the one-row move (secret scan,
//	     client-repo projection, role guard), ONE write at --to and ONE
//	     write at --from; any refused row refuses the whole batch first.

// BulkFlags are the bulk form's flags of row.move.
type BulkFlags struct {
	From   string   `json:"from,omitempty" flag:"from" desc:"bulk move: every row of this store or folder (<store>[/<folder>], repo or me), in one commit at --to and one at --from"`
	Where  []string `json:"where,omitempty" flag:"where" desc:"with --from: only rows matching this filter (field=value, field~=text, created_at<2026-06-01, table=memories; repeatable, all must match)"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"with --from: list what would move, write nothing"`
}

// MovedRow is one row of a bulk move: its id, table and the store it left.
type MovedRow struct {
	ID    string `json:"id"`
	Table string `json:"table"`
	From  string `json:"from"`
	Title string `json:"title,omitempty"`
}

// ManyMover is the Env upgrade the bulk move needs (wire.App): the
// attached stores and their rows, the store name a flag value means, and
// engine.MoveMany behind the wire's guards. An Env without it answers
// E_UNSUPPORTED for --from.
type ManyMover interface {
	Stores() []store.Source
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	AttachedStore(ctx context.Context, name store.Source) (store.Source, error)
	MoveMany(ctx context.Context, refs []engine.Ref, to store.Source, opt engine.MoveOptions) (engine.MovedMany, error)
	// PlanMoveSecrets is engine.PlanMoveSecrets for a dry run: the secret
	// refusal the real move would give, before it says "would move"
	// (devco D-717). Nothing is written.
	PlanMoveSecrets(ctx context.Context, refs []engine.Ref, to store.Source, rerun string) error
}

// NearDuplicateFinder is the optional Env upgrade (wire.App) that lists
// the rows a bulk move would bring into --to that repeat a row already
// there (devco D-724: same name, or most body words in common), as store
// adopt does. An Env without it lists none; the move is the same.
type NearDuplicateFinder interface {
	MoveNearDuplicates(ctx context.Context, moving []row.Row, to store.Source) ([]opsstores.NearDuplicate, error)
}

// placeTables are the tables a bulk move leaves where they are: the
// place's own bookkeeping (who writes there, its project and repos, its
// policy, metadata keys, kinds, secret rules, tags and scopes), which
// belongs to the folder, not to the notes in it. `lore row move --id`
// still moves one of them on purpose. Machine-local, global-key and
// team-profile tables are left too (movableTables).
var placeTables = map[row.Table]bool{
	schema.TableProjects: true, "repos": true, "policies": true, "meta_keys": true, "meta_values": true,
	"kinds": true, "kind_fields": true, "vars": true, "secret_rules": true, "secret_allows": true,
	"tags": true, "scopes": true, "teams": true,
}

// movableTables lists the tables a bulk move takes rows from, in schema
// order: every synced table except placeTables, machine-local tables (they
// never leave the machine), global-key tables (actors, machines: one id
// everywhere) and team-profile tables (never in a client repo, and the
// roster's own).
func movableTables() []row.Table {
	var out []row.Table
	for _, d := range schema.Builtin() {
		if !d.Synced || d.MachineLocal || d.GlobalKey || d.NeverClient || placeTables[d.Name] {
			continue
		}
		out = append(out, d.Name)
	}
	return out
}

// bulk reports whether in asks for the --from form, refusing a mix of
// the two forms and the bulk flags without --from.
func (in MoveIn) bulk() (bool, error) {
	id, from := strings.TrimSpace(in.ID), strings.TrimSpace(in.From)
	switch {
	case from != "" && id != "":
		return false, errs.New(errs.InvalidInput, "move: give --id <id> or --from <store>/<folder>, not both")
	case from != "" && in.WithChildren:
		return false, errs.New(errs.InvalidInput, "move --from: every row of the folder moves; --with-children is for one task (--id)")
	case from != "":
		return true, nil
	case len(in.Where) > 0 || in.DryRun:
		return false, errs.New(errs.InvalidInput, "move: --where and --dry-run go with --from").
			WithHint("lore move --from <store>/<folder> --to <place> --dry-run lists what would move")
	case id == "":
		return false, errs.New(errs.InvalidInput, "move: give --id <id> or --from <store>/<folder>").
			WithHint("lore move --id <id> --to <place>, or lore move --from <store>/<folder> --to <place> --dry-run")
	}
	return false, nil
}

// line is the command that runs this bulk move, as printed in hints and
// next steps: every flag named, --where values quoted, and the global
// --setup the caller gave (from the home folder with two org setups the
// line without it is refused, so it would not run where it is printed:
// devco D-712).
func (in MoveIn) line(ctx context.Context, yes bool) string {
	parts := []string{"lore move --from " + strings.TrimSpace(in.From) + " --to " + strings.TrimSpace(in.To)}
	for _, w := range in.Where {
		parts = append(parts, "--where "+shellQuote(w))
	}
	if in.DropInternal {
		parts = append(parts, "--drop-internal")
	}
	if wt := strings.TrimSpace(in.WithTime); wt != "" {
		parts = append(parts, "--with-time "+wt)
	}
	if yes {
		parts = append(parts, "--yes")
	}
	return strings.Join(parts, " ") + setupFlag(ctx)
}

// bulkLine is the printed line of the bulk move planned in p, naming
// both places in full (profilestore.PlaceTo): a linked repo attaches its
// folder under the bare org name, so `--from acme/web` arrives here as
// "acme", and a line saying `--from acme` moves another place wherever an
// admin runs it (devco D-697, D-712).
func (o ops) bulkLine(ctx context.Context, in MoveIn, p bulkPlan) string {
	in.From = profilestore.PlaceTo(ctx, o.env, p.from)
	in.To = profilestore.PlaceTo(ctx, o.env, p.to)
	return in.line(ctx, true)
}

// setupFlag is " --setup <name>" when the caller named a setup, "": a
// printed command keeps the setup it was run with.
func setupFlag(ctx context.Context) string {
	if s := strings.TrimSpace(op.SetupOf(ctx)); s != "" {
		return " --setup " + s
	}
	return ""
}

// moveAsk is ctx carrying the move's own refusal wording for the write
// guard (profilestore.WithAsk): without it a member's move into a company
// folder was refused with the one-row add of the first moved row
// (`lore memory add --body … --to <org>/org --yes`), a line that copies one
// note instead of moving them (devco D-712).
func moveAsk(ctx context.Context, does, line string) context.Context {
	return profilestore.WithAsk(ctx, does, line)
}

// shellQuote single-quotes s for a printed command.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// bulkPlan is what a bulk move would move: the resolved stores and rows.
type bulkPlan struct {
	from, to store.Source
	hits     []profilestore.Placed
}

// planMany resolves --from and --to and lists the matching live rows of
// --from (table, then id order).
func (o ops) planMany(ctx context.Context, in MoveIn) (ManyMover, bulkPlan, error) {
	mm, ok := o.env.(ManyMover)
	if !ok {
		return nil, bulkPlan{}, errs.New(errs.Unsupported, "move --from: this build's store wiring cannot move rows in bulk")
	}
	to := strings.TrimSpace(in.To)
	if to == "" {
		return nil, bulkPlan{}, errs.New(errs.InvalidInput, "move: --to is required (a store name from .lore/stores.yaml, or repo)")
	}
	from, err := mm.AttachedStore(ctx, store.Source(strings.TrimSpace(in.From)))
	if err != nil {
		return nil, bulkPlan{}, err
	}
	if from == store.Source(to) {
		return nil, bulkPlan{}, errs.New(errs.InvalidInput, "move --from: "+string(from)+" and --to are the same store; nothing to move").
			WithHint("name another attached store with --to (`lore store list` shows them)")
	}
	tables := movableTables()
	match, err := wheredel.Across(in.Where, tables, "move")
	if err != nil {
		return nil, bulkPlan{}, err
	}
	p := bulkPlan{from: from, to: store.Source(to)}
	for _, t := range tables {
		m, ok := match[t]
		if !ok {
			continue
		}
		rows, lerr := mm.List(ctx, from, t)
		if lerr != nil {
			return nil, bulkPlan{}, lerr
		}
		slices.SortFunc(rows, func(a, b row.Row) int { return strings.Compare(a.ID.String(), b.ID.String()) })
		for _, r := range rows {
			if !r.Deleted && m(r) {
				p.hits = append(p.hits, profilestore.Placed{Source: from, Row: r})
			}
		}
	}
	return mm, p, nil
}

// moveMany is the bulk form of row.move (see the file comment).
func (o ops) moveMany(ctx context.Context, in MoveIn) (MoveOut, error) {
	wt := engine.TimeShare(strings.TrimSpace(in.WithTime))
	if wt != "" && !slices.Contains(engine.TimeShares(), string(wt)) {
		return MoveOut{}, errs.New(errs.InvalidInput, "move: --with-time takes "+strings.Join(engine.TimeShares(), " or "))
	}
	mm, p, err := o.planMany(ctx, in)
	if err != nil {
		return MoveOut{}, err
	}
	// the full places (a linked folder arrives as the bare org name, D-697)
	out := MoveOut{From: profilestore.PlaceTo(ctx, o.env, p.from), To: profilestore.PlaceTo(ctx, o.env, p.to), DryRun: in.DryRun}
	for _, h := range p.hits {
		def, _ := o.env.TableSchema(ctx, h.Row.Table)
		out.Rows = append(out.Rows, MovedRow{ID: h.Row.ID.String(), Table: string(h.Row.Table), From: string(h.Source), Title: opstrash.Title(h.Row, def, true)})
	}
	if len(p.hits) == 0 {
		return out, nil
	}
	// the one-row move's company-row check, for every row at once (D-659)
	if err = profilestore.RequireAdminForRows(ctx, o.env, p.hits, "move", o.bulkLine(ctx, in, p), ""); err != nil {
		return MoveOut{}, err
	}
	refs := make([]engine.Ref, len(p.hits))
	for i, h := range p.hits {
		refs[i] = engine.Ref{Store: h.Source, Table: h.Row.Table, ID: h.Row.ID}
	}
	// D-724: read before the move (after it the rows are in --to, one
	// copy each), listed by the dry run and the run alike
	if out.Duplicates, err = o.nearDuplicates(ctx, p); err != nil {
		return MoveOut{}, err
	}
	out.setup = setupFlag(ctx)
	if in.DryRun {
		// the real run's secret refusal comes first (D-717): a preview
		// never prints a next step that would be refused
		if err = mm.PlanMoveSecrets(ctx, refs, p.to, o.bulkLine(ctx, in, p)); err != nil {
			return MoveOut{}, err
		}
		out.next = o.bulkLine(ctx, in, p)
		return out, nil
	}
	does := "move " + strconv.Itoa(len(refs)) + " row(s) from " + profilestore.PlaceTo(ctx, o.env, p.from) + " to " + profilestore.PlaceTo(ctx, o.env, p.to)
	mv, err := mm.MoveMany(moveAsk(ctx, does, o.bulkLine(ctx, in, p)), refs, p.to, engine.MoveOptions{DropInternal: in.DropInternal, WithTime: wt, SecretRerun: o.bulkLine(ctx, in, p)})
	if err != nil {
		return MoveOut{}, err
	}
	for _, w := range mv.Rows {
		for _, wn := range w.Warnings {
			msg := w.Row.ID.String() + ": " + wn.Message
			switch wn.Code {
			case engine.WarnTimeKeptBack, engine.WarnInternalKeptBack, engine.WarnTimeTotals:
				out.Notes = append(out.Notes, msg)
			default:
				out.Warnings = append(out.Warnings, msg)
			}
		}
	}
	return out, nil
}

// nearDuplicates is what the Env's NearDuplicateFinder lists for the
// rows of p moving into p.to (nil without one).
func (o ops) nearDuplicates(ctx context.Context, p bulkPlan) ([]opsstores.NearDuplicate, error) {
	f, ok := o.env.(NearDuplicateFinder)
	if !ok {
		return nil, nil
	}
	moving := make([]row.Row, len(p.hits))
	for i, h := range p.hits {
		moving[i] = h.Row
	}
	return f.MoveNearDuplicates(ctx, moving, p.to)
}

// moveManyQuestion is the bulk move's question: where the rows go and who
// will see them, how many, and the first rows by title. A dry run asks
// nothing (engine needsConfirm).
func (o ops) moveManyQuestion(ctx context.Context, in MoveIn) (string, error) {
	_, p, err := o.planMany(ctx, in)
	if err != nil {
		return "", err
	}
	if len(p.hits) == 0 {
		return "", nil // the move answers "nothing matches" without asking
	}
	if err = profilestore.RequireAdminForRows(ctx, o.env, p.hits, "move", o.bulkLine(ctx, in, p), ""); err != nil {
		return "", err // refused before any question (D-659)
	}
	var b strings.Builder
	b.WriteString("This moves " + strconv.Itoa(len(p.hits)) + " row(s) from " + string(p.from) + "; they become visible to everyone with access to " + string(p.to))
	if c, ok := o.env.(Classer); ok {
		if cl, cerr := c.StoreClass(ctx, p.to); cerr == nil && cl != "" {
			b.WriteString(" (" + string(cl) + ")")
		}
	}
	b.WriteString(":\n")
	for i, h := range p.hits {
		if i == bulkListed {
			b.WriteString("  … and " + strconv.Itoa(len(p.hits)-bulkListed) + " more\n")
			break
		}
		def, _ := o.env.TableSchema(ctx, h.Row.Table)
		b.WriteString("  " + strconv.Quote(opstrash.Title(h.Row, def, true)) + " (" + h.Row.ID.String() + ")\n")
	}
	b.WriteString("Every row keeps its id; one commit in " + string(p.to) + " and one in " + string(p.from) + ". Move them?")
	return b.String(), nil
}

// bulkListed is how many rows the question and the text name one by one.
const bulkListed = 20

// bulkText is the human form of a bulk move (or its dry run).
func (o MoveOut) bulkText() string {
	var b strings.Builder
	n := strconv.Itoa(len(o.Rows))
	switch {
	case len(o.Rows) == 0:
		b.WriteString("no live row in " + o.From + " matches; nothing to move\n")
		return b.String()
	case o.DryRun:
		b.WriteString("would move " + n + " row(s) from " + o.From + " to " + o.To + " (nothing written, --dry-run):\n")
	default:
		b.WriteString("✓ moved " + n + " row(s) from " + o.From + " to " + o.To + " (same ids; one commit in each store; the old copies are tombstoned)\n")
	}
	counts := map[string]int{}
	for _, r := range o.Rows {
		counts[r.Table]++
	}
	tables := make([]string, 0, len(counts))
	for t := range counts {
		tables = append(tables, t)
	}
	slices.Sort(tables)
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = t + " " + strconv.Itoa(counts[t])
	}
	b.WriteString("  " + strings.Join(parts, ", ") + "\n")
	if o.DryRun {
		for i, r := range o.Rows {
			if i == bulkListed {
				b.WriteString("  … and " + strconv.Itoa(len(o.Rows)-bulkListed) + " more (--json lists every row)\n")
				break
			}
			b.WriteString("  " + r.ID + "  " + r.Title + "\n")
		}
	}
	// the moved row's bare id names one live row once moved (the old
	// copy is a tombstone), so no "once pushed" condition (D-724)
	for _, l := range (opsstores.NearDupPrint{Place: o.To, From: o.From, Dry: o.DryRun, Suffix: o.setup}).Lines(o.Duplicates) {
		b.WriteString(l + "\n")
	}
	for _, n := range o.Notes {
		b.WriteString("  " + n + "\n")
	}
	for _, w := range o.Warnings {
		b.WriteString("  warning: " + w + "\n")
	}
	if o.next != "" {
		b.WriteString("next: " + o.next + "\n")
	}
	return b.String()
}
