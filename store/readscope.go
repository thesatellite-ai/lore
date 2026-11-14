package store

import "context"

// ReadScope marks one command (one operation run): backends may remember
// what they learned about their files (which files git reports changed)
// for the rest of the scope instead of asking again for every read, as
// long as nothing was written meanwhile (perf3). Outside a scope nothing
// is remembered, so a long-lived process (an App kept open, `lore api`)
// always sees a file changed around it.
type ReadScope struct{ _ byte } // non-zero size: every scope has its own address

type readScopeKey struct{}

// WithReadScope returns ctx inside a new ReadScope.
func WithReadScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, readScopeKey{}, &ReadScope{})
}

// ReadScopeOf is ctx's ReadScope, nil outside one.
func ReadScopeOf(ctx context.Context) *ReadScope {
	s, _ := ctx.Value(readScopeKey{}).(*ReadScope)
	return s
}
