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
	// Move is the whole "move this repo's notes into the store" job in one
	// command (devco D-713): the adoption, --remove-source (the row files
	// and the adopted rows' copies on the repo's live ref), and the link of
	// this repo to the folder (lore org link), leaving one repo diff lore's
	// own hook takes; it prints the git command left, or runs it with
	// --commit. Without it each step was its own command (adopt, org link,
	// a row move per live row, LORE_ALLOW_ROW_DROP).
	Move bool `json:"move,omitempty" flag:"move" desc:"move the repo's notes into the folder in one go: adopt, remove the source (row files and live copies), link this repo to the folder; prints the git command left (or runs it with --commit)"`
	// Own links the repo as one of the org's own repos (--move only), as
	// `lore org link --own` does.
	Own bool `json:"own,omitempty" flag:"own" desc:"with --move: the repo is one of the org's own repos (lore org link --own)"`
	// Commit makes the repo commit of the removal itself, through the
	// repo's hooks, instead of printing the git command (--move or
	// --remove-source). It refuses when other changes are staged.
	Commit bool `json:"commit,omitempty" flag:"commit" desc:"with --move or --remove-source: commit the removal in the repo (through its hooks) instead of printing the git command"`
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
	// Duplicates are the adopted rows that repeat a row the folder
	// already held (devco D-724): listed, both kept, never merged.
	Duplicates []NearDuplicate `json:"duplicates,omitempty"`
	// Actors names each actor of the repo mapped onto the org roster's
	// actor of the same identity (stable key): "act_A (human:x) → act_B".
	// The mapped row is not written; the adopted rows name act_B.
	Actors []string `json:"actors,omitempty"`
	// Refs names each ref field an adopted row held as a name, not an id
	// (devco D-766: a v0.1.17 task with tasklist_id "Backlog"), and what
	// it holds now: that row's id, or nothing.
	Refs []string `json:"refs,omitempty"`
	// Removed counts the row files --remove-source deleted from the repo.
	Removed int `json:"removed,omitempty"`
	// LiveRemoved counts the adopted rows --remove-source took off the
	// repo's live ref (refs/lore/live) (devco D-716: left there, each id was
	// "two live rows with one id" once the repo read the folder); LiveKept
	// names the ones that changed meanwhile and stay (rerun to take them).
	LiveRemoved int      `json:"live_removed,omitempty"`
	LiveKept    []string `json:"live_kept,omitempty"`
	// Tidied are the repo's lore files --move removed beside the row files
	// (devco D-719), slash paths from the repo's top: the stale
	// .lore/LORE.md (a linked repo renders none, and a client repo's leak
	// guard refused every commit touching it), and in a repo still on the
	// lore 0.1.17 format its project row and _meta.json, so nothing of the
	// old format is left there (read, never migrated). AdoptTidyWhy says
	// why each went.
	Tidied []string `json:"tidied,omitempty"`
	// Link is what --move's `lore org link` of the repo to the folder says
	// (its plan on a dry run).
	Link []string `json:"link,omitempty"`
	// Next are the commands left to run, exactly as they run (the repo
	// commit of the removal when --commit was not given).
	Next []string `json:"next,omitempty"`
	// HeldTwice counts the adopted rows the repo keeps live beside their
	// folder copy (devco:D-939, bug hunt H-200): an adopt without
	// --remove-source into a folder the repo reads (a folder of a store
	// it attaches whole), so a command by id on one answers
	// E_AMBIGUOUS_ID and lore doctor is DEGRADED (copied_id) until one
	// copy goes. Settle is the one command that settles them all: the
	// same adopt with --remove-source (the newest copy stays, in the
	// folder). Both empty when nothing is held twice.
	HeldTwice int    `json:"held_twice,omitempty"`
	Settle    string `json:"settle,omitempty"`
	// Commit is the repo commit --commit made ("" when none).
	Commit string `json:"commit,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
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
// --move's link adds the codes of `lore org link` (an offline or
// ambiguous org, an org that disagrees with the repo's).
var adoptErrors = append(append([]errs.Code{}, visibilityErrors...), errs.Refused, errs.PolicyDenied, errs.NotProjectRoot, errs.Stale,
	errs.Offline, errs.OrgAmbiguous, errs.OrgDisagrees, errs.Unsupported, errs.SecretDetected)

// SplitTarget splits "<store>/<folder>" at its last slash (a store url
// holds slashes, a folder name never does). A bare "<folder>" has no
// store: the folder is looked up across the org's stores (folder names
// are unique there), so store is "".
func SplitTarget(verb, target string) (string, string, error) {
	i := strings.LastIndex(target, "/")
	if target == "" || i == len(target)-1 || i == 0 {
		return "", "", errs.New(errs.InvalidInput, verb+": "+quote(target)+" names no folder; give <store>/<folder> or a folder of the org").
			WithHint("e.g. `lore store adopt --target team/client_north --dry-run` (`lore store folder list` shows the folders)")
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
			{Title: "see what would move", CLI: "lore store adopt --target team/client_north --from ~/code/north-app --dry-run", Want: "would adopt"},
			{Title: "copy the repo you are in", CLI: "lore store adopt --target team/client_north --yes", Want: "adopted"},
			{Title: "move the repo you are in, end to end, and commit it", CLI: "lore store adopt --target team/client_north --move --commit --yes", Want: "adopted"},
		},
	}, func(ctx context.Context, in AdoptIn) (AdoptOut, error) {
		in.Target, in.From = strings.TrimSpace(in.Target), strings.TrimSpace(in.From)
		if in.Move {
			in.RemoveSource = true // a move removes the source; --move says it all
		}
		if in.Own && !in.Move {
			return AdoptOut{}, errs.New(errs.InvalidInput, verb+": --own says how --move links the repo; it does nothing without --move").
				WithHint("lore store adopt --target " + in.Target + " --move --own")
		}
		if in.Commit && !in.RemoveSource {
			return AdoptOut{}, errs.New(errs.InvalidInput, verb+": --commit commits the removal of the adopted row files; without --move or --remove-source the repo is not changed").
				WithHint("lore store adopt --target " + in.Target + " --move --commit")
		}
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
				WithHint("adopt into a client or project folder: lore store folder add --store " + st + " --folder <folder>")
		}
		ad, ok := upgrade.As[Adopter](m)
		if !ok {
			return AdoptOut{}, errs.New(errs.Internal, verb+": this build cannot adopt a repo into a store")
		}
		plan := in
		plan.DryRun = true
		p, err := ad.Adopt(ctx, plan, st, folder)
		p.Settle = AdoptSettleCommand(in, p.HeldTwice)
		if err != nil || in.DryRun {
			return p, err
		}
		if p.Written == 0 && (!in.RemoveSource || p.Removed == 0 && p.LiveRemoved == 0) && !in.Move {
			p.DryRun, p.Removed, p.LiveRemoved = false, 0, 0 // nothing to write or remove: the run is this answer
			p.Next = nil
			return p, nil
		}
		if !engine.Confirmed(ctx) {
			q := adoptQuestion(p, in)
			return AdoptOut{}, errs.New(errs.NeedsConfirm, verb+" "+in.Target+": "+q).
				WithQuestion(q).WithHint("ask the person, then rerun with --yes")
		}
		out, err := ad.Adopt(ctx, in, st, folder)
		out.Settle = AdoptSettleCommand(in, out.HeldTwice)
		return out, err
	})
}

// AdoptSettleCommand is the one command that settles the rows an adopt
// left held twice (devco:D-939): the same adopt with --remove-source,
// which writes the newer repo copies into the folder and takes the
// repo's copies out (a diff the person commits). "" when none is.
func AdoptSettleCommand(in AdoptIn, heldTwice int) string {
	if heldTwice == 0 || in.RemoveSource {
		return ""
	}
	c := "lore store adopt --target " + cdArg(in.Target)
	if in.From != "" {
		c += " --from " + cdArg(in.From)
	}
	return c + " --remove-source --yes"
}

// AdoptCommitCommand is the repo commit of an adoption's removal, as
// adopt prints it and --commit runs it: the row files only (`add -A
// .lore/data`), a message naming where the rows went. One spelling for the
// printed line, the JSON next step and the run, so what is printed is what
// works.
// The tidied files outside .lore/data (D-719: .lore/LORE.md) are staged
// with it, named each, since `git add` of a path that is gone and was
// never tracked fails.
func AdoptCommitCommand(top, place string, tidied ...string) string {
	top = cdArg(top) // one shell word also when the repo path holds a space (devco D-724)
	return "git -C " + top + " add -A " + strings.Join(AdoptCommitPaths(tidied), " ") + " && git -C " + top + " commit -m \"" + AdoptCommitMessage(place) + "\""
}

// AdoptCommitPaths are the pathspecs of that commit's `git add -A`:
// .lore/data, then each tidied file outside it.
func AdoptCommitPaths(tidied []string) []string {
	out := []string{AdoptDataDir}
	for _, p := range tidied {
		if !strings.HasPrefix(p, AdoptDataDir+"/") {
			out = append(out, p)
		}
	}
	return out
}

// AdoptDataDir is the repo's row folder from its top, slash separated.
const AdoptDataDir = ".lore/data"

// AdoptLoreMD is the repo's rendered context file, which --move removes
// when lore generated it (D-719).
const AdoptLoreMD = ".lore/LORE.md"

// AdoptTidyOutside are the files outside .lore/data that --move may
// remove (adoptTidy): what a commit of the removal must stage beside
// .lore/data, and what doctor's commit line looks for (D-719).
func AdoptTidyOutside() []string { return []string{AdoptLoreMD} }

// AdoptTidyWhy is why --move removed the tidied file p (D-719).
func AdoptTidyWhy(p string) string {
	switch {
	case p == AdoptLoreMD:
		return "lore renders no LORE.md in a repo linked to a store folder, and the stale one kept old notes in the repo"
	case strings.HasSuffix(p, "/_meta.json"):
		return "the repo's lore 0.1.17 format marker: no notes are left in the repo, so nothing needs migrating"
	default:
		return "the repo's own lore 0.1.17 place row: the folder is its own project"
	}
}

// AdoptCommitMessage is the subject of that repo commit.
func AdoptCommitMessage(place string) string { return "lore: move the notes to " + place }

// adoptQuestion is the confirmation: what is written where, and how.
func adoptQuestion(p AdoptOut, in AdoptIn) string {
	del := "delete " + plural(p.Removed, "adopted row file") + " from " + p.From + "/.lore/data"
	if p.LiveRemoved > 0 {
		del += " and take " + plural(p.LiveRemoved, "adopted row") + " off its live ref"
	}
	if in.Commit {
		del += ", committed in the repo"
	} else {
		del += " (you review and commit that)"
	}
	if in.Move {
		del += ", and link the repo to " + p.Store + "/" + p.Folder
	}
	if p.Written == 0 {
		return p.Store + "/" + p.Folder + " already holds every row of " + p.From + " (no store commit); " + del + "?"
	}
	q := "copy " + plural(p.Written, "row") + " of " + p.From + " into " + p.Store + "/" + p.Folder + " (" + p.ProjectID + ")? one store commit, " + pushPhrase(p.Push)
	if in.RemoveSource {
		q += "; then " + del
	}
	if p.HeldTwice > 0 {
		q += "; " + plural(p.HeldTwice, "row") + " stay live in the repo too, held twice (commands by id on them ask which copy to keep; --remove-source takes the repo's copies out)"
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
	for _, l := range o.Refs {
		b.WriteString("  warning: " + l + "\n")
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
	b.WriteString(o.heldTwiceText())
	switch {
	case o.LiveRemoved > 0 && o.DryRun:
		b.WriteString("  would take " + plural(o.LiveRemoved, "adopted row") + " off the repo's live ref (refs/lore/live)\n")
	case o.LiveRemoved > 0:
		b.WriteString("  took " + plural(o.LiveRemoved, "adopted row") + " off the repo's live ref (refs/lore/live): they live in " + o.Store + "/" + o.Folder + " now\n")
	}
	for _, k := range o.LiveKept {
		b.WriteString("  left on the live ref (changed while it moved; rerun the same command to take it): " + k + "\n")
	}
	switch {
	case o.Removed > 0 && o.DryRun:
		b.WriteString("  would remove " + plural(o.Removed, "row file") + " from " + o.From + "/.lore/data (--remove-source)\n")
	case o.Removed > 0 && o.Commit != "":
		b.WriteString("  removed " + plural(o.Removed, "row file") + " from " + o.From + "/.lore/data and committed it (" + o.Commit + "); push it with the repo as usual\n")
	case o.Removed > 0:
		b.WriteString("  removed " + plural(o.Removed, "row file") + " from " + o.From + "/.lore/data: review and commit it (" + AdoptCommitCommand(o.From, o.Store+"/"+o.Folder, o.Tidied...) + "); lore's hook knows the rows live there now\n")
	}
	verbTidy := "removed "
	if o.DryRun {
		verbTidy = "would remove "
	}
	for _, p := range o.Tidied {
		b.WriteString("  " + verbTidy + p + " (" + AdoptTidyWhy(p) + ")\n")
	}
	if o.Removed == 0 && len(o.Tidied) > 0 && !o.DryRun {
		if o.Commit != "" {
			b.WriteString("  committed it (" + o.Commit + "); push it with the repo as usual\n")
		} else {
			b.WriteString("  review and commit it (" + AdoptCommitCommand(o.From, o.Store+"/"+o.Folder, o.Tidied...) + ")\n")
		}
	}
	// a moved row's bare id names the folder's copy at once, also before
	// the repo commit is pushed (devco D-713); a dry run reads the moving
	// row in its repo, where it still is: a cd into that repo's top first,
	// since --from names a repo other than the folder the command ran in
	// (devco D-724)
	dup := NearDupPrint{Place: o.Store + "/" + o.Folder, Dry: o.DryRun}
	if o.DryRun {
		dup.From, dup.RunIn = "repo", o.From
	}
	for _, l := range dup.Lines(o.Duplicates) {
		b.WriteString(l + "\n")
	}
	for _, l := range o.Link {
		b.WriteString("  link: " + strings.TrimPrefix(strings.TrimSpace(l), "link: ") + "\n")
	}
	if o.DryRun {
		for _, c := range o.Next {
			b.WriteString("  then the repo commit: " + c + "\n")
		}
	}
	if o.Note != "" {
		b.WriteString("  " + o.Note + "\n")
	}
	return b.String()
}

// heldTwiceText says the rows the adopt leaves live in the repo and in
// the folder it reads, and the one command that settles them (D-939).
func (o AdoptOut) heldTwiceText() string {
	if o.HeldTwice == 0 || o.Settle == "" {
		return ""
	}
	place := o.Store + "/" + o.Folder
	n := plural(o.HeldTwice, "row")
	if o.DryRun {
		return "  " + n + " would stay live in " + o.From + " too, held twice with " + place + " (this repo reads that folder): commands by id on them would ask which copy to keep (E_AMBIGUOUS_ID) and lore doctor would be DEGRADED (copied_id); adopt and take the repo's copies out in one go: " + o.Settle + "\n"
	}
	return "  " + n + " now held twice, in " + o.From + " and in " + place + " (this repo reads that folder): commands by id on them ask which copy to keep (E_AMBIGUOUS_ID) and lore doctor is DEGRADED (copied_id) until one copy goes; settle them all at once (the newest copy stays, in " + place + "; the repo's copies go): " + o.Settle + "\n"
}
