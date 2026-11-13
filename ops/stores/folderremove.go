package stores

// folderremove.go — `lore store folder remove <store> <folder> [--purge]`
// (owner report 2026-11-11: folders made in the org store by mistake, whose
// names are then needed in another store of the org, since folder names
// are unique across an org's stores).
//
//	validate names ─▶ Manager.FolderContents (which store, may this
//	                  machine remove its folders, rows, trash, links)
//	  ├▶ no such folder / the org folder ─▶ E_NOT_FOUND / E_REFUSED
//	  ├▶ rows or links, no --purge ─▶ E_REFUSED with the counts and the
//	  │                               next steps (lore move, lore org
//	  │                               unlink, or --purge)
//	  ├▶ not confirmed ─▶ E_NEEDS_CONFIRM naming what goes: --yes for an
//	  │                   empty folder; --purge needs the literal
//	  │                   --confirm (DQ2: --yes never purges)
//	  └▶ Manager.RemoveFolder: an empty folder goes in one store commit;
//	     --purge first moves the live rows into the store's trash (one
//	     bat_), drops the links and the folder's _meta.json, keeps the
//	     trashed row files for `lore trash restore`; pushed per the
//	     store's push mode
//
// Who may: the org's admin for an org store, whoever can push for any
// other store (the remote refuses the rest); agents.person_only applies
// as for every operation (the engine checks it).

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpFolderRemove removes a folder from a store.
const OpFolderRemove = "store.folder.remove"

// FolderRemoveIn is the input of store.folder.remove.
type FolderRemoveIn struct {
	Store  string `json:"store" arg:"0" desc:"the store (team, me, an org's name, an org store's url, or a .lore/stores.yaml name)"`
	Folder string `json:"folder" arg:"1" desc:"the folder to remove"`
	Purge  bool   `json:"purge,omitempty" flag:"purge" desc:"also remove a folder that holds rows or repo links: its rows go to the store's trash first, its links are dropped (needs --confirm)"`
	// Reason is the delete reason the purged rows carry (default "store
	// folder remove --purge"); policy delete.require_reason is met either way.
	Reason string `json:"reason,omitempty" flag:"reason" desc:"with --purge: why the rows go to the trash (stored as their deleted_reason; default: store folder remove --purge)"`
	// Confirm is the literal --confirm a purge needs (DQ2: --yes never
	// purges): the folder's links and id go for good.
	Confirm bool `json:"confirm,omitempty" flag:"confirm" desc:"required with --purge: the folder's repo links and id go for good"`
}

// FolderContents is what a folder holds, read before anything is removed.
type FolderContents struct {
	// Store is the name the output gives the store (storeLabel).
	Store  string `json:"store"`
	Folder string `json:"folder"`
	// ProjectID is the folder's prj_ id (its _meta.json).
	ProjectID string `json:"id"`
	// Rows counts the folder's live rows; the copies of the roster's
	// actors every write leaves in a folder are not counted (actors are
	// never deleted; they go with the folder).
	Rows int `json:"rows"`
	// Trashed counts rows already in the folder's trash.
	Trashed int `json:"trashed"`
	// Links counts the repo links in the folder's _meta.json (`lore org
	// link`).
	Links int `json:"links"`
	// Push is how the removal reaches the remote: the store's push mode
	// ("now", "batch", "manual"; "" for a folder store, which has none).
	Push string `json:"push,omitempty"`
}

// Empty reports whether the folder holds no rows (live or trashed) and no
// repo links: such a folder is removed without --purge.
func (c FolderContents) Empty() bool { return c.Rows == 0 && c.Trashed == 0 && c.Links == 0 }

// FolderRemoveOut is the output of store.folder.remove.
type FolderRemoveOut struct {
	Store     string `json:"store"`
	Folder    string `json:"folder"`
	ProjectID string `json:"id"`
	// Rows, Trashed and Links are what the folder held (FolderContents).
	Rows    int `json:"rows"`
	Trashed int `json:"trashed"`
	Links   int `json:"links"`
	// Push is the store's push mode ("" for a folder store).
	Push string `json:"push,omitempty"`
	// Purged is true when --purge removed a folder that held rows or links.
	Purged bool `json:"purged,omitempty"`
	// Batch is the bat_ id the purged rows went into the trash under.
	Batch string `json:"batch,omitempty"`
	// Pushed is true when the commit reached the remote now; false for a
	// folder store, an offline remote, or a batch/manual push mode (Note
	// says which).
	Pushed bool   `json:"pushed"`
	Note   string `json:"note,omitempty"`
}

// RemovedOut is the output of removing the folder c describes.
func RemovedOut(c FolderContents) FolderRemoveOut {
	return FolderRemoveOut{Store: c.Store, Folder: c.Folder, ProjectID: c.ProjectID, Rows: c.Rows, Trashed: c.Trashed, Links: c.Links, Push: c.Push, Purged: !c.Empty()}
}

// contents is the FolderContents the output reports.
func (o FolderRemoveOut) contents() FolderContents {
	return FolderContents{Store: o.Store, Folder: o.Folder, ProjectID: o.ProjectID, Rows: o.Rows, Trashed: o.Trashed, Links: o.Links, Push: o.Push}
}

// FolderRemover is the optional Manager upgrade behind store.folder.remove.
type FolderRemover interface {
	// FolderContents finds the store, checks that this machine may remove
	// its folders (an org store: the org's admin) and that folder is one
	// it may remove (not the org folder), and counts what it holds. A
	// folder the store does not have is E_NOT_FOUND.
	FolderContents(ctx context.Context, store, folder string) (FolderContents, error)
	// RemoveFolder removes it (the op has checked and asked): see the file
	// comment.
	RemoveFolder(ctx context.Context, in FolderRemoveIn, c FolderContents) (FolderRemoveOut, error)
}

// folderRemoveErrors are the codes of store.folder.remove.
var folderRemoveErrors = append(append([]errs.Code{}, visibilityErrors...), errs.Refused, errs.PolicyDenied, errs.HasDependents)

// folderRemoveOp is store.folder.remove bound to m.
func folderRemoveOp(m Manager) op.Entry {
	return op.Define(op.Spec{
		Name: OpFolderRemove, Summary: "Remove a folder from a store: an empty one at once; one with rows or repo links only with --purge --confirm (its rows go to the trash first); asks first",
		Permission: op.PermAdmin, Access: op.AccessRead, Errors: folderRemoveErrors,
		Confirm: op.ConfirmRule{MayAsk: true},
		Examples: []op.Example{
			{Title: "an empty folder made by mistake", CLI: "lore store folder remove --store team --folder client_old --yes", Want: "removed folder client_old"},
			{Title: "a folder with rows: they go to the trash first", CLI: "lore store folder remove --store team --folder client_old --purge --confirm", Want: "trash"},
		},
	}, func(ctx context.Context, in FolderRemoveIn) (FolderRemoveOut, error) {
		in.Store, in.Folder = strings.TrimSpace(in.Store), strings.TrimSpace(in.Folder)
		if err := CheckStoreArg(in.Store); err != nil {
			return FolderRemoveOut{}, err
		}
		if err := checkSnakeFolder("store folder remove", in.Folder); err != nil {
			return FolderRemoveOut{}, err
		}
		if in.Folder == rowfs.FolderOrg {
			return FolderRemoveOut{}, errs.New(errs.Refused, "store folder remove: "+quote(in.Folder)+" is the org folder's name: it holds the org's roster, teams and lore-org.yaml and is never removed")
		}
		fr, ok := upgrade.As[FolderRemover](m)
		if !ok {
			return FolderRemoveOut{}, errs.New(errs.Internal, "store folder remove: this build cannot remove store folders")
		}
		c, err := fr.FolderContents(ctx, in.Store, in.Folder)
		if err != nil {
			return FolderRemoveOut{}, err
		}
		if !c.Empty() && !in.Purge {
			return FolderRemoveOut{}, notEmptyErr(c)
		}
		if !c.Empty() && !in.Confirm {
			// DQ2: a purge needs the literal --confirm; --yes never purges
			q := removeQuestion(c)
			return FolderRemoveOut{}, errs.New(errs.NeedsConfirm, "store folder remove "+c.Store+" "+c.Folder+" --purge: "+q).
				WithQuestion(q).WithHint("ask the person, then rerun with --confirm (--yes does not purge)").
				WithNext("lore", "store", "folder", "remove", in.Store, in.Folder, "--purge", "--confirm")
		}
		if c.Empty() && !engine.Confirmed(ctx) {
			q := removeQuestion(c)
			return FolderRemoveOut{}, errs.New(errs.NeedsConfirm, "store folder remove "+c.Store+" "+c.Folder+": "+q).
				WithQuestion(q).WithHint("ask the person, then rerun with --yes")
		}
		return fr.RemoveFolder(ctx, in, c)
	})
}

// notEmptyErr refuses a folder with rows or links without --purge, naming
// the counts and each way on (exact errors: the cause and the next step).
func notEmptyErr(c FolderContents) error {
	what := contentsPhrase(c)
	var next []string
	if c.Rows > 0 || c.Trashed > 0 {
		next = append(next, "move the rows you keep with `lore move --id <id> --to <store>/<folder>`")
	}
	if c.Links > 0 {
		next = append(next, "unlink each repo with `lore org unlink --org <org> --project "+c.Folder+"` run in that repo")
	}
	next = append(next, "or remove it with everything in it: `lore store folder remove --store "+c.Store+" --folder "+c.Folder+" --purge` (the rows go to the store's trash first)")
	return errs.New(errs.Refused, "store folder remove: folder "+c.Folder+" of "+c.Store+" holds "+what+"; nothing was removed").
		WithHint(strings.Join(next, "; "))
}

// removeQuestion is the confirmation: what goes and how it is saved.
func removeQuestion(c FolderContents) string {
	if c.Empty() {
		return "remove the empty folder " + c.Folder + " (" + c.ProjectID + ") from " + c.Store + "? one store commit, " + pushPhrase(c.Push)
	}
	return "remove folder " + c.Folder + " (" + c.ProjectID + ") from " + c.Store + " with " + contentsPhrase(c) +
		"? the live rows go to the store's trash first (`lore trash restore` brings them back once the folder is made again), the links are dropped; " + pushPhrase(c.Push)
}

// contentsPhrase is "3 rows (1 in the trash) and 2 repo links".
func contentsPhrase(c FolderContents) string {
	var parts []string
	if n := c.Rows + c.Trashed; n > 0 {
		p := plural(n, "row")
		if c.Trashed > 0 {
			p += " (" + strconv.Itoa(c.Trashed) + " in the trash)"
		}
		parts = append(parts, p)
	}
	if c.Links > 0 {
		parts = append(parts, plural(c.Links, "repo link"))
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " and ")
}

// pushPhrase says when the removal reaches the remote.
func pushPhrase(mode string) string {
	switch mode {
	case "":
		return "written in the folder store"
	case string(storepush.ModeNow):
		return "pushed at once"
	default:
		return "pushed with the store's next " + mode + " push (`lore sync` pushes it now)"
	}
}

// plural is "1 row" / "2 rows".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// Text is the result line, and for a purge how to get the rows back.
func (o FolderRemoveOut) Text() string {
	var b strings.Builder
	b.WriteString("✓ removed folder " + o.Folder + " (" + o.ProjectID + ") from " + o.Store)
	if o.Purged {
		b.WriteString(" with " + contentsPhrase(o.contents()))
	}
	b.WriteString("\n")
	if o.Purged && o.Batch != "" {
		b.WriteString("  its rows are in the store's trash under " + o.Batch + "; to bring them back: `lore store folder add --store " + o.Store + " --folder " + o.Folder + "`, attach the folder to a repo, then `lore trash restore --id " + o.Batch + "`\n")
	}
	if o.Note != "" {
		b.WriteString("  " + o.Note + "\n")
	}
	return b.String()
}
