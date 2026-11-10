package store

import "github.com/thesatellite-ai/lore/model/ids"

// SourceRef is the `_source` object every `--json` row from an attached
// store carries: which store and which folder of it the row lives in, by
// name and by permanent id.
//
// FLAG1 (owner C): `_source` is an object {store, store_id, folder,
// folder_id} instead of the bare store name, because names are labels that
// can change while the sto_ / prj_ ids are permanent, so a script can key
// on the ids and still show the names. `_label` keeps the human
// "[store/folder]" text. Rows of the repo's own store carry no `_source`
// (as before FLAG1), so a repo row's bytes are unchanged.
//
// StoreID is absent for a store whose lore-store.yaml has no id (a v0.1.17
// marker); Folder and FolderID are absent for a store attached at its root
// whose folder has no identity yet. No compatibility shim: there are no
// outside users of the old string form.
type SourceRef struct {
	// Store is the attached store's name (stores.yaml / engine Stores()).
	Store string `json:"store"`
	// StoreID is the store's sto_ id from its lore-store.yaml marker.
	StoreID ids.ID[ids.Store] `json:"store_id,omitempty,omitzero"`
	// Folder is the store folder the row lives in ("" = the store root).
	Folder string `json:"folder,omitempty"`
	// FolderID is that folder's prj_ id (its _meta.json project_id).
	FolderID ids.ID[ids.Project] `json:"folder_id,omitempty,omitzero"`
}

// SourceDescriber is the optional upgrade an engine host (wire.App)
// implements so operations can fill `_source` with the ids: it describes
// the attached store name as a SourceRef. A host without it yields
// SourceRef{Store: name} (FLAG1: the name alone is still a valid object).
type SourceDescriber interface {
	DescribeSource(name Source) SourceRef
}
