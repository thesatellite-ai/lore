package settings

import (
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/plural"
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
	org, _, _ := strings.Cut(string(sc.Source), "/")
	names := make([]string, 0, len(from))
	for _, s := range from {
		names = append(names, string(s))
	}
	return fmt.Sprintf("folder %s: the resources stored there and those of %s whose %s is %s; %s (org-wide or another folder's) are not listed: lore resource list --store %s",
		sc.Source, strings.Join(names, ", "), resProjectField, sc.Folder, plural.Count(left, "more resource"), org)
}

// folderScope is the folder this read's resource lists keep to (ok false:
// every attached store's resources).
func (o ops) folderScope() (FolderScope, bool) {
	if v, ok := o.env.(ResourceFolderScoper); ok {
		return v.ResourceFolderScope()
	}
	return FolderScope{}, false
}
