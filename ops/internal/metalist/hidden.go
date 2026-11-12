package metalist

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/dirscope"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Hidden counts the live rows a list left out, by why, so the list can
// say so under its rows (owner report 2026-11-12: `decision list` showed
// 245 of 251 adopted decisions, the 6 scoped to their v0.1.17 repo, with
// nothing saying why, and `--all` did not exist).
type Hidden struct {
	// Repo: scoped to a repo the list's scope does not take (by default a
	// list shows the project-wide rows; --repo, --all-repos widen it).
	Repo int `json:"repo,omitempty"`
	// Folder: scoped to a folder of a repo (a dir: scope) the list's scope
	// does not take; Folders names those folders, in first-seen order. A
	// folder-scoped row also carries its repo, so without this split the
	// footer called it "scoped to a repo" (bug hunt B3-10).
	Folder  int      `json:"folder,omitempty"`
	Folders []string `json:"folders,omitempty"`
	// Archived: archived rows of a list that hides them.
	Archived int `json:"archived,omitempty"`
}

// maxFolderNames is how many folder names the footer spells out; more
// end in "…".
const maxFolderNames = 3

// OutOfScope counts one row the list's repo scope left out: under Folder
// when dir (its dir: scope's folder, "" for none) is set, else under Repo.
func (h *Hidden) OutOfScope(dir string) {
	if dir == "" {
		h.Repo++
		return
	}
	h.Folder++
	if !slices.Contains(h.Folders, dir) {
		h.Folders = append(h.Folders, dir)
	}
}

// Note is the footer line naming what was hidden and how to see it (""
// when nothing was): "6 hidden (6 scoped to a repo); --all shows them",
// "1 hidden (1 scoped to a folder (dir:src/booking)); --all shows them".
func (h Hidden) Note() string {
	var parts []string
	if h.Repo > 0 {
		parts = append(parts, strconv.Itoa(h.Repo)+" scoped to a repo")
	}
	if h.Folder > 0 {
		names := make([]string, 0, len(h.Folders))
		for i, d := range h.Folders {
			if i == maxFolderNames {
				names = append(names, "…")
				break
			}
			names = append(names, dirscope.Spec{Kind: dirscope.KindDir, Path: d}.Label())
		}
		word := " scoped to a folder"
		if len(h.Folders) > 1 {
			word = " scoped to folders"
		}
		parts = append(parts, strconv.Itoa(h.Folder)+word+" ("+strings.Join(names, ", ")+")")
	}
	if h.Archived > 0 {
		parts = append(parts, strconv.Itoa(h.Archived)+" archived")
	}
	if len(parts) == 0 {
		return ""
	}
	return strconv.Itoa(h.Repo+h.Folder+h.Archived) + " hidden (" + strings.Join(parts, ", ") + "); --all shows them"
}

// Notes appends the footer to a list's notes (unchanged when nothing was
// hidden).
func (h Hidden) Notes(notes []string) []string {
	if n := h.Note(); n != "" {
		return append(notes, n)
	}
	return notes
}

// ScopeLister reads a store's rows of one table (every list Env has it).
type ScopeLister interface {
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
}

// Dirs resolves a row's scope_id to its dir: scope's folder, reading
// each store's scopes table at most once, and only when a hidden row
// has a scope (most lists never read it).
type Dirs struct {
	l     ScopeLister
	paths map[store.Source]map[string]string
}

// NewDirs is a Dirs reading through l.
func NewDirs(l ScopeLister) *Dirs {
	return &Dirs{l: l, paths: map[store.Source]map[string]string{}}
}

// Of is the folder of r's dir: scope in src ("" when r has no scope, the
// scope is gone or the scopes cannot be read: the footer then says
// "scoped to a repo", which is still true).
func (d *Dirs) Of(ctx context.Context, src store.Source, r row.Row) string {
	v, ok := r.Fields[schema.FieldScope]
	if !ok || v.Kind() == value.KindNull || v.String() == "" {
		return ""
	}
	m, ok := d.paths[src]
	if !ok {
		m = map[string]string{}
		rows, err := d.l.List(ctx, src, schema.TableScopes)
		if err == nil {
			for _, s := range rows {
				if p, ok := s.Fields[schema.FieldScopePath]; ok && !s.Deleted && p.Kind() != value.KindNull {
					m[s.ID.String()] = p.String()
				}
			}
		}
		d.paths[src] = m
	}
	return m[v.String()]
}
