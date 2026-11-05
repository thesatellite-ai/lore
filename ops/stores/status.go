package stores

// store list status columns (CLI_REFERENCE 3.21: "list shows each attached
// store with … its visibility, whether this machine can reach it"). Flow:
//
//	store.list → Manager.ListStores → StatusReader.StoreStatus per entry
//	           (optional upgrade; local reads only, no network: the list
//	           runs on a repo-only engine so an unreachable store never
//	           blocks it, spec 7 store commands)

import "context"

// Reach is how this machine can reach a store, from local reads only.
type Reach string

// The reach values.
const (
	// ReachOK is a file:// store whose folder holds its marker.
	ReachOK Reach = "ok"
	// ReachLocalCopy is a git store with a clone here: reads work offline;
	// the remote is not contacted by `store list` (DECISION STORE-LIST-REACH).
	ReachLocalCopy Reach = "local_copy"
	// ReachNoLocalCopy is a git store never cloned here: the first read
	// needs the network (E_STORE_UNREACHABLE when offline).
	ReachNoLocalCopy Reach = "no_local_copy"
	// ReachMissing is a file:// store whose folder or marker is gone.
	ReachMissing Reach = "missing"
)

// Status is the visibility and reach of one listed store. Visibility is
// "" when the marker cannot be read here (no local copy).
type Status struct {
	Visibility string
	Reach      Reach
}

// StatusReader is the optional Manager upgrade behind the VISIBILITY and
// REACH columns; without it the columns stay empty.
type StatusReader interface {
	StoreStatus(ctx context.Context, e Entry) Status
}
