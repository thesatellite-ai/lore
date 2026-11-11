package stores

// adopt.go — `lore store adopt <store>/<folder> [--from <repo>]` (owner
// report 2026-11-11): move a whole repo's lore data into a store folder in
// one pass, keeping every id, HLC and relation, as ONE store commit and one
// push. Before it the only id-keeping path was `lore move <id>` one row at
// a time (a store commit and a push per row, minutes per row in a 5,000-row
// repo), so the largest repos could not be moved at all.
//
// Why a store operation and not `lore move --all`: move is a per-row edit
// (a new HLC, a `moves` entry in _o, a tombstone left at the source, the
// target's per-row checks) and runs inside the repo it empties. Adopting a
// repo is a copy of its data as it is: the same bytes but the project the
// rows belong to, the same HLCs, the source left untouched unless asked
// (--remove-source writes the removal as an ordinary diff the person
// commits), from any folder of the setup like the other store commands
// (it never edits the repo it runs in). One command per folder keeps it
// idempotent: run again, it writes nothing.
//
//	validate ─▶ Adopter.Adopt(dry run): what the repo holds (.lore/data,
//	            the v0.1.17 live ref, a v0.1.17 lore.db) and what the
//	            folder already has
//	  ├▶ nothing new ─▶ "nothing to adopt" (no commit, no push)
//	  ├▶ not confirmed ─▶ E_NEEDS_CONFIRM naming the counts (--yes)
//	  └▶ Adopter.Adopt: one store commit, pushed per the store's push mode;
//	     --remove-source then deletes the adopted row files from the repo

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpAdopt copies a repo's lore data into a store folder.
const OpAdopt = "store.adopt"

// AdoptIn is the input of store.adopt.
type AdoptIn struct {
	Target string `json:"target" arg:"0" desc:"<store>/<folder>: the store (team, an org's name, an org store's url, or a .lore/stores.yaml name) and its folder (made first with lore store folder add); an org's name, or a folder named alone, means whichever of the org's stores holds that folder"`
	From   string `json:"from,omitempty" flag:"from" desc:"the repo whose lore data moves (default: the repo this command runs in)"`
	// RemoveSource deletes the adopted row files from the repo's
	// .lore/data once the store commit is made: a normal diff the person
	// reviews and commits. Without it the repo is never changed.
	RemoveSource bool `json:"remove_source,omitempty" flag:"remove-source" desc:"after the store commit, delete the adopted row files from the repo's .lore/data (an ordinary diff you commit)"`
	DryRun       bool `json:"dry_run,omitempty" flag:"dry-run" desc:"count what would be written; change nothing"`
}

// AdoptOut is the output of store.adopt.
type AdoptOut struct {
	Store  string `json:"store"`
	Folder string `json:"folder"`
	// ProjectID is the folder's prj_ id: the project every adopted row
	// with a project now belongs to.
	ProjectID string `json:"id"`
	// From is the repo top the rows came from.
	From string `json:"from"`
	// Written counts the rows written into the folder (new, or a newer
	// copy of a row the folder held older); Unchanged the rows the folder
	// already held byte for byte; Kept the rows it holds newer or deleted
	// (never overwritten or brought back).
	Written   int `json:"written"`
	Unchanged int `json:"unchanged"`
	Kept      int `json:"kept"`
	// Tables counts the written rows per table, by table name.
	Tables []AdoptTable `json:"tables,omitempty"`
	// Live counts the adopted rows that came from the repo's live ref
	// (refs/lore/live: open work v0.1.17 and 0.1.18 keep there), LoreDB
	// the ones only the repo's v0.1.17 lore.db held.
	Live   int `json:"live,omitempty"`
	LoreDB int `json:"lore_db,omitempty"`
	// Left names what stays in the repo, with why ("projects: 1 (the
	// folder is its own project)").
	Left []string `json:"left,omitempty"`
	// Relinked counts the --supersedes links lore 0.1.16 and older wrote
	// on the new row that the adoption moved onto the old row (the shape
	// since v0.1.17); Conflicts names the rows two links claim (left as
	// they are: one row has one successor, the person picks).
	Relinked  int      `json:"relinked,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	// Actors names each actor of the repo mapped onto the org roster's
	// actor of the same identity (stable key): "act_A (human:x) → act_B".
	// The mapped row is not written; the adopted rows name act_B.
	Actors []string `json:"actors,omitempty"`
	// Removed counts the row files --remove-source deleted from the repo.
	Removed int  `json:"removed,omitempty"`
	DryRun  bool `json:"dry_run,omitempty"`
	// Push is the store's push mode ("" for a folder store); Pushed is
	// true when the commit reached the remote now; Note says otherwise why.
	Push   string `json:"push,omitempty"`
	Pushed bool   `json:"pushed"`
	Note   string `json:"note,omitempty"`
}

// AdoptTable is how many rows of one table an adoption writes.
type AdoptTable struct {
	Table string `json:"table"`
	Rows  int    `json:"rows"`
}

// Adopter is the optional Manager upgrade behind store.adopt.
type Adopter interface {
	// Adopt reads the repo in.From and the folder of store, and (unless
	// in.DryRun) writes every row the folder lacks or holds older as one
	// store commit, then with in.RemoveSource deletes the adopted row
	// files from the repo. The op has validated and asked.
	Adopt(ctx context.Context, in AdoptIn, store, folder string) (AdoptOut, error)
}

// adoptErrors are the codes of store.adopt.
var adoptErrors = append(append([]errs.Code{}, visibilityErrors...), errs.Refused, errs.PolicyDenied, errs.NotProjectRoot, errs.Stale)

// SplitTarget splits "<store>/<folder>" at its last slash (a store url
// holds slashes, a folder name never does). A bare "<folder>" has no
// store: the folder is looked up across the org's stores (folder names
// are unique there), so store is "".
func SplitTarget(verb, target string) (string, string, error) {
	i := strings.LastIndex(target, "/")
	if target == "" || i == len(target)-1 || i == 0 {
		return "", "", errs.New(errs.InvalidInput, verb+": "+quote(target)+" names no folder; give <store>/<folder> or a folder of the org").
			WithHint("e.g. lore store adopt team/client_north (`lore store folder list` shows the folders)")
	}
	if i < 0 {
		return "", target, nil
	}
	return target[:i], target[i+1:], nil
}

// adoptOp is store.adopt bound to m.
func adoptOp(m Manager) op.Entry {
	const verb = "store adopt"
	return op.Define(op.Spec{
		Name: OpAdopt, Summary: "Copy a repo's lore data (row files, v0.1.17 live rows) into a store folder in one commit, keeping every id and HLC; asks first",
		Permission: op.PermWrite, Access: op.AccessRead, Errors: adoptErrors,
		Confirm: op.ConfirmRule{MayAsk: true},
		Examples: []op.Example{
			{Title: "see what would move", CLI: "lore store adopt team/client_north --from ~/code/north-app --dry-run", Want: "would adopt"},
			{Title: "move the repo you are in", CLI: "lore store adopt team/client_north --yes", Want: "adopted"},
		},
	}, func(ctx context.Context, in AdoptIn) (AdoptOut, error) {
		in.Target, in.From = strings.TrimSpace(in.Target), strings.TrimSpace(in.From)
		st, folder, err := SplitTarget(verb, in.Target)
		if err != nil {
			return AdoptOut{}, err
		}
		if err := CheckStoreArg(st); st != "" && err != nil {
			return AdoptOut{}, err
		}
		if err := checkSnakeFolder(verb, folder); err != nil {
			return AdoptOut{}, err
		}
		if folder == rowfs.FolderOrg {
			return AdoptOut{}, errs.New(errs.Refused, verb+": "+quote(folder)+" is the org folder: it holds the org's roster, teams and lore-org.yaml, not a repo's notes").
				WithHint("adopt into a client or project folder: lore store folder add " + st + " <folder>")
		}
		ad, ok := upgrade.As[Adopter](m)
		if !ok {
			return AdoptOut{}, errs.New(errs.Internal, verb+": this build cannot adopt a repo into a store")
		}
		plan := in
		plan.DryRun = true
		p, err := ad.Adopt(ctx, plan, st, folder)
		if err != nil || in.DryRun {
			return p, err
		}
		if p.Written == 0 && (!in.RemoveSource || p.Removed == 0) {
			p.DryRun, p.Removed = false, 0 // nothing to write or remove: the run is this answer
			return p, nil
		}
		if !engine.Confirmed(ctx) {
			q := adoptQuestion(p, in.RemoveSource)
			return AdoptOut{}, errs.New(errs.NeedsConfirm, verb+" "+in.Target+": "+q).
				WithQuestion(q).WithHint("ask the person, then rerun with --yes")
		}
		return ad.Adopt(ctx, in, st, folder)
	})
}

// adoptQuestion is the confirmation: what is written where, and how.
func adoptQuestion(p AdoptOut, remove bool) string {
	del := "delete " + plural(p.Removed, "adopted row file") + " from " + p.From + "/.lore/data (you review and commit that)"
	if p.Written == 0 {
		return p.Store + "/" + p.Folder + " already holds every row of " + p.From + " (no store commit); " + del + "?"
	}
	q := "copy " + plural(p.Written, "row") + " of " + p.From + " into " + p.Store + "/" + p.Folder + " (" + p.ProjectID + ")? one store commit, " + pushPhrase(p.Push)
	if remove {
		q += "; then " + del
	}
	return q
}

// Text is the result: the counts, what stays, and the next step.
func (o AdoptOut) Text() string {
	var b strings.Builder
	verb := "✓ adopted "
	if o.DryRun {
		verb = "would adopt "
	}
	switch {
	case o.Written == 0 && o.DryRun:
		b.WriteString("nothing to adopt: " + o.Store + "/" + o.Folder + " already holds every row of " + o.From + "\n")
	case o.Written == 0:
		b.WriteString("✓ nothing to adopt: " + o.Store + "/" + o.Folder + " already holds every row of " + o.From + " (no commit)\n")
	default:
		b.WriteString(verb + plural(o.Written, "row") + " of " + o.From + " into " + o.Store + "/" + o.Folder + " (" + o.ProjectID + "), ids and HLCs kept\n")
		parts := make([]string, 0, len(o.Tables))
		for _, t := range o.Tables {
			parts = append(parts, t.Table+" "+strconv.Itoa(t.Rows))
		}
		b.WriteString("  " + strings.Join(parts, ", ") + "\n")
	}
	if o.Live > 0 {
		b.WriteString("  from the repo's live ref (refs/lore/live): " + strconv.Itoa(o.Live) + "\n")
	}
	if o.LoreDB > 0 {
		b.WriteString("  only in its v0.1.17 .lore/lore.db: " + strconv.Itoa(o.LoreDB) + "\n")
	}
	if o.Unchanged > 0 || o.Kept > 0 {
		b.WriteString("  already there: " + strconv.Itoa(o.Unchanged) + " unchanged, " + strconv.Itoa(o.Kept) + " newer or deleted in the folder (kept)\n")
	}
	for _, l := range o.Actors {
		b.WriteString("  actor " + l + " (the same person; references rewritten)\n")
	}
	if o.Relinked > 0 {
		b.WriteString("  " + plural(o.Relinked, "--supersedes link") + " written by lore 0.1.16 or older moved onto the row each replaces\n")
	}
	for _, c := range o.Conflicts {
		b.WriteString("  left as is: " + c + "\n")
	}
	for _, l := range o.Left {
		b.WriteString("  left in the repo: " + l + "\n")
	}
	if o.Removed > 0 && o.DryRun {
		b.WriteString("  would remove " + plural(o.Removed, "row file") + " from " + o.From + "/.lore/data (--remove-source)\n")
	} else if o.Removed > 0 {
		b.WriteString("  removed " + plural(o.Removed, "row file") + " from " + o.From + "/.lore/data: review and commit it (git -C " + o.From + " add -A .lore/data && git -C " + o.From + " commit)\n")
	}
	if o.Note != "" {
		b.WriteString("  " + o.Note + "\n")
	}
	return b.String()
}
