package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// ResourceViewer is an optional Env upgrade: the stores whose resources
// this place may read. wire.App answers only the repo store in a client
// repo (RES 3.9: no store resources in a client repo's reads, even on an
// agency machine with the store attached). An Env without it reads every
// attached store.
type ResourceViewer interface {
	ResourceStores() []store.Source
}

// resStores is where resource reads (show, list, lint, report, bulk
// matches, repoint, graph) look. Referrer checks and kind-field use checks
// still read every store: a store row may point at a client row, and a
// delete must not strand it.
func (o ops) resStores() []store.Source {
	if v, ok := o.env.(ResourceViewer); ok {
		return v.ResourceStores()
	}
	return engine.ReadStoresOf(o.env)
}

// ResourceFolderScoper is an optional Env upgrade (devco D-583): the one
// org-store folder a read works on (`--store <org>/<folder>`, or a store
// clone's folder) while the org's company folder is attached beside it
// for its kinds. wire.App answers it; an Env without it lists every
// attached store's resources.
type ResourceFolderScoper interface {
	ResourceFolderScope() (FolderScope, bool)
}

// FolderScope is the folder a resource list is scoped to.
type FolderScope struct {
	// Source is the folder's attached store name (acme/sync).
	Source store.Source
	// Folder is the folder's name in its store (sync).
	Folder string
}

// resProjectField is the resource field naming the project folder a
// resource of the company folder belongs to (`project = sync`): the
// convention the org's repo and service kinds use.
const resProjectField = "project"

// keeps reports whether a resource read from src belongs to the folder:
// stored there, or stored beside it (the company folder) with its project
// field naming the folder (sync, or acme/sync).
func (sc FolderScope) keeps(src store.Source, r Resource) bool {
	if src == sc.Source {
		return true
	}
	p := strings.TrimSpace(r.Values[resProjectField])
	return p == sc.Folder || p == string(sc.Source)
}

// note is the line under a folder-scoped list that left rows out: which
// rows it shows and the read that lists the whole org.
func (sc FolderScope) note(left int, from []store.Source) string {
	return sc.noteFor(left, from, OpResList)
}

// noteFor is note naming the read that left the rows out (resource
// stale, resource report: devco D-586), so its whole-org form is that
// same read with --store <org>.
func (sc FolderScope) noteFor(left int, from []store.Source, opName string) string {
	org, _, _ := strings.Cut(string(sc.Source), "/")
	names := make([]string, 0, len(from))
	for _, s := range from {
		names = append(names, string(s))
	}
	return fmt.Sprintf("folder %s: the resources stored there and those of %s whose %s is %s; %s (org-wide or another folder's) are not listed: lore %s --store %s",
		sc.Source, strings.Join(names, ", "), resProjectField, sc.Folder, plural.Count(left, "more resource"), strings.ReplaceAll(opName, ".", " "), org)
}

// folderScope is the folder this read's resource lists keep to (ok false:
// every attached store's resources).
func (o ops) folderScope() (FolderScope, bool) {
	if v, ok := o.env.(ResourceFolderScoper); ok {
		return v.ResourceFolderScope()
	}
	return FolderScope{}, false
}

// inFolderScope narrows rows to the folder this read is scoped to (the
// rule resource list keeps, FolderScope.keeps): resource stale and report
// read through `--store <org>/<folder>` listed every resource of the
// org's company folder too (devco D-586). It returns the rows kept, how
// many it left out and their stores; rows unchanged when the read is not
// folder-scoped.
func (o ops) inFolderScope(ctx context.Context, rows []located) ([]located, int, []store.Source) {
	sc, scoped := o.folderScope()
	if !scoped {
		return rows, 0, nil
	}
	kept := rows[:0:0]
	left := 0
	var from []store.Source
	for _, r := range rows {
		if o.rowInScope(ctx, sc, r) {
			kept = append(kept, r)
			continue
		}
		left++
		if !slices.Contains(from, r.Src) {
			from = append(from, r.Src)
		}
	}
	return kept, left, from
}

// rowInScope reports whether the resource r belongs to the folder sc: a
// row whose kind this read cannot decode counts only where it is stored.
func (o ops) rowInScope(ctx context.Context, sc FolderScope, r located) bool {
	if r.Src == sc.Source {
		return true
	}
	v, err := o.view(ctx, r, false)
	return err == nil && sc.keeps(r.Src, v)
}

// resourceInScope is resourceByName for a read or write naming one
// resource through a folder-scoped open (`resource check|graph --id <name>
// --store <org>/<folder>`, devco D-586): a resource of another folder (or
// org-wide) is refused naming where it is and the whole-org form of the
// command, as resource list leaves it out.
func (o ops) resourceInScope(ctx context.Context, key, opName string) (located, error) {
	r, err := o.resourceByName(ctx, key, "")
	if err != nil {
		return located{}, err
	}
	sc, scoped := o.folderScope()
	if !scoped || o.rowInScope(ctx, sc, r) {
		return r, nil
	}
	org, _, _ := strings.Cut(string(sc.Source), "/")
	return located{}, errs.New(errs.NotFound, fmt.Sprintf("resource %s is not one of folder %s: it is stored in %s and its %s is not %s",
		quote(key), sc.Source, r.Src, resProjectField, sc.Folder)).
		WithHint(scopeHint(opName, key, org))
}

// scopeHint is the way out of resourceInScope's refusal: the same command
// with --store <org> when its id is all it takes (check, graph, archive),
// else the flag and the read that lists every resource of the org.
func scopeHint(opName, key, org string) string {
	switch opName {
	case OpResCheck, OpResGraph, OpResArchive:
		return fmt.Sprintf("name the org instead: lore %s --id %s --store %s", strings.ReplaceAll(opName, ".", " "), key, org)
	}
	return fmt.Sprintf("name the org instead with --store %s; lore resource list --store %s lists every resource of the org", org, org)
}
