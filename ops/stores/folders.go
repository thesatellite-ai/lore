package stores

// folders.go — `lore store folder list [store]` and which store commands
// need only the setup (owner report 2026-11-11: an admin keeps the client
// folders in a plain parent directory, not a git repo, and makes and lists
// the team store's folders from there).
//
//	store folder list            ─▶ every store the setup sees (the org's
//	                                stores, me; inside a repo also its
//	                                .lore/stores.yaml stores), one block
//	                                per store, each store listed once
//	store folder list <store>    ─▶ that store (team, me, an org's name,
//	                                an org store's url, a stores.yaml name)
//
// A row is one folder that has its _meta.json: name, prj_ id, who created
// it and when (the commit that added that file; a folder store keeps no
// history, so neither), whether this repo attaches it, and its row files. The root (".") is not a folder.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpFolderList lists the folders of a store.
const OpFolderList = "store.folder.list"

// FolderListIn is the input of store.folder.list.
type FolderListIn struct {
	Store string `json:"store,omitempty" arg:"0" desc:"the store (team, me, an org's name, an org store's url, or a .lore/stores.yaml name); default: every store the setup sees"`
}

// FolderListOut is the output of store.folder.list.
type FolderListOut struct {
	Folders []FolderRow `json:"folders"`
	Count   int         `json:"count"`
	// Stores are the stores read, in the order listed (a store with no
	// folder yet is still named here).
	Stores []string `json:"stores"`
}

// FolderRow is one folder of a store.
type FolderRow struct {
	// Store is the name the store is known by here (team's org name, me,
	// a stores.yaml name).
	Store  string `json:"store"`
	Folder string `json:"folder"`
	// ID is the folder's permanent prj_ id (its _meta.json).
	ID string `json:"id"`
	// CreatedBy and CreatedAt (RFC 3339, UTC) come from the store commit
	// that added the folder's _meta.json; both are empty for a folder
	// store (file://), which keeps no history.
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	// Attached is true when the repo this command runs in attaches the
	// folder (its .lore/stores.yaml, or the folder its org link names);
	// always false outside a repo.
	Attached bool `json:"attached"`
	// Rows counts the folder's row files (a directory walk, no parse).
	Rows int `json:"rows"`
}

// FolderLister is the optional Manager upgrade behind store.folder.list.
type FolderLister interface {
	// ListFolders lists the folders of the store name, or of every store
	// the setup sees when name is "".
	ListFolders(ctx context.Context, name string) (FolderListOut, error)
}

// folderListOp is store.folder.list bound to m.
func folderListOp(m Manager) op.Entry {
	return op.Define(op.Spec{
		Name: OpFolderList, Summary: "List the folders of a store (default: every store the setup sees), and which ones this repo attaches",
		Permission: op.PermRead, Access: op.AccessRead, Errors: visibilityErrors,
		Examples: []op.Example{
			{Title: "every store's folders", CLI: "lore store folder list", Want: "folders"},
			{Title: "the team store's folders", CLI: "lore store folder list team", Want: "folders"},
			{Title: "as JSON", CLI: "lore store folder list team --json", Want: `"folders"`},
		},
	}, func(ctx context.Context, in FolderListIn) (FolderListOut, error) {
		name := strings.TrimSpace(in.Store)
		if err := CheckStoreArg(name); name != "" && err != nil {
			return FolderListOut{}, err
		}
		fl, ok := upgrade.As[FolderLister](m)
		if !ok {
			return FolderListOut{}, errs.New(errs.Internal, "store folder list: this build cannot list store folders")
		}
		out, err := fl.ListFolders(ctx, name)
		if err != nil {
			return FolderListOut{}, err
		}
		if out.Folders == nil {
			out.Folders = []FolderRow{}
		}
		if out.Stores == nil {
			out.Stores = []string{}
		}
		out.Count = len(out.Folders)
		return out, nil
	})
}

// IsStoreURL reports whether a store argument is a url (an org store named
// by its address) rather than a name: it has a scheme or is a git scp form
// (user@host:path). Names are snake_case, so neither can be one.
func IsStoreURL(arg string) bool {
	if strings.Contains(arg, "://") {
		return true
	}
	at := strings.Index(arg, "@")
	colon := strings.Index(arg, ":")
	return at > 0 && colon > at+1
}

// CheckStoreArg checks the store argument of the commands that write or
// read one store (store folder add|list): team, me, a store name, or an
// org store's url (whose scheme must have a backend). Which store it is,
// and whether it needs a repo here, the Manager answers.
func CheckStoreArg(arg string) error {
	switch {
	case arg == attach.ReservedTeam:
		return nil
	case IsStoreURL(arg):
		return CheckScheme(arg)
	}
	return CheckName(arg, true)
}

// Noun is the first word of every store operation name.
const Noun = "store"

// LeavesRepo reports whether the store operation name never changes the
// repo it runs in: every store operation except the ones that edit its
// .lore/stores.yaml (init, add, remove, set-url). The host then does not
// set that repo's git up (merge driver, .gitattributes, hooks,
// .gitignore lines) for it: a command that changes a store must not
// rewrite an unrelated repo it happens to run in.
func LeavesRepo(name string) bool {
	return strings.HasPrefix(name, Noun+".") && !WritesStoresFile(name) && name != OpRemove
}

// RunsWithoutRepo reports whether the store operation name needs only the
// setup, so it runs the same outside any git repo (from the parent folder
// of an org's client folders) and never changes the repo it runs in: it
// reads or writes a store, ~/.lore/config.yaml or this machine's clones,
// never the repo's .lore/stores.yaml. init, add, remove and set-url write
// that file and keep needing a repo (`store init me` excepted, see wire).
func RunsWithoutRepo(name string) bool {
	return slices.Contains([]string{OpList, OpVisibility, OpFolderAdd, OpFolderList, OpFolderRemove, OpAdopt, OpSet, OpCompact}, name)
}

// noneShown fills an empty column of the folder table.
const noneShown = "-"

// Text is one block per store: its folders as a table (folder, id, created
// by, created, attached here, rows), or a line saying it has none yet.
func (o FolderListOut) Text() string {
	if len(o.Stores) == 0 {
		return "no stores: this folder is in no setup with a team or personal store (`lore setup list`)\n"
	}
	var b strings.Builder
	for i, s := range o.Stores {
		if i > 0 {
			b.WriteString("\n")
		}
		var rows [][]string
		for _, f := range o.Folders {
			if f.Store != s {
				continue
			}
			by, at, here := f.CreatedBy, f.CreatedAt, "no"
			if by == "" {
				by = noneShown
			}
			if at == "" {
				at = noneShown
			}
			if f.Attached {
				here = "yes"
			}
			rows = append(rows, []string{f.Folder, f.ID, by, at, here, strconv.Itoa(f.Rows)})
		}
		if len(rows) == 0 {
			b.WriteString("store " + s + ": no folders yet (`lore store folder add " + s + " <folder>`)\n")
			continue
		}
		b.WriteString("store " + s + " folders:\n")
		writeTable(&b, []string{"FOLDER", "ID", "CREATED BY", "CREATED", "ATTACHED HERE", "ROWS"}, rows)
	}
	return b.String()
}

// writeTable writes head and rows as left-aligned columns, two spaces
// apart, each line indented by two.
func writeTable(b *strings.Builder, head []string, rows [][]string) {
	width := make([]int, len(head))
	for _, r := range append([][]string{head}, rows...) {
		for i, c := range r {
			width[i] = max(width[i], utf8.RuneCountInString(c))
		}
	}
	for _, r := range append([][]string{head}, rows...) {
		b.WriteString(" ")
		for i, c := range r {
			b.WriteString(" " + c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", width[i]-utf8.RuneCountInString(c)+1))
			}
		}
		b.WriteString("\n")
	}
}
