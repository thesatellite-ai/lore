package orgcfg

import (
	"context"
	"slices"
	"sync"
)

// routeLog records every url a test Router is asked for. GitLinks.prefetch
// fetches an org's stores at the same time (owner item M-4) and calls the
// Router from each goroutine, so a stub that appends to a bare slice is a
// data race (devco D-774); every recording stub goes through this guard.
type routeLog struct {
	mu   sync.Mutex
	urls []string
}

// router is a Router that records u, then answers with route (nil: no
// options, as git is configured).
func (l *routeLog) router(route func(u string) (args, env []string)) Router {
	return func(_ context.Context, u string) ([]string, []string) {
		l.mu.Lock()
		l.urls = append(l.urls, u)
		l.mu.Unlock()
		if route == nil {
			return nil, nil
		}
		return route(u)
	}
}

// asked is a copy of the urls recorded so far.
func (l *routeLog) asked() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.urls)
}

// reset forgets the urls recorded so far.
func (l *routeLog) reset() {
	l.mu.Lock()
	l.urls = nil
	l.mu.Unlock()
}
