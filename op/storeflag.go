package op

// StoreFlag is the --store flag a read takes to name where it reads
// (devco D-499, widened: every read names its store as `rule list
// --store <org>` does): `<org>` reads every folder of the org's stores,
// `<org>/<folder>` that folder, `repo` this repo's store, any other name
// the store of that name. The read's operation does not filter by it: the
// composition root opens the named store alone for the read (wire
// runOutsideRepo), so the rows it sees are that store's. An input embeds
// it; StoreRouter tells the root the read is routed so.
type StoreFlag struct {
	Store string `json:"store,omitempty" flag:"store" alias:"to" desc:"read this store: <org> (every folder of the org), <org>/<folder>, repo, or a store's name (default every attached store)"`
}

// RoutedStore implements StoreRouter.
func (f StoreFlag) RoutedStore() string { return f.Store }

// StoreRouter is the method StoreFlag gives an input: the store its read
// is routed to by the composition root ("" for none).
type StoreRouter interface {
	RoutedStore() string
}

// RoutedStore returns the store an input names through StoreRouter; ok is
// false when the input has no such method or names none.
func (i Input) RoutedStore() (name string, ok bool) {
	if !i.v.IsValid() || !i.v.CanInterface() {
		return "", false
	}
	// reflect.Value.Interface returns the stdlib's untyped value; it is
	// narrowed to StoreRouter on this line and never kept untyped.
	r, isRouter := i.v.Interface().(StoreRouter)
	if !isRouter {
		return "", false
	}
	n := r.RoutedStore()
	return n, n != ""
}
