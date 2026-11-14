package wire

// hostwiring.go — what the host's hooks wrote to a clone's tracked git
// wiring (.gitignore, .gitattributes) during one operation, and how lore fix
// asks them to write it again (devco N-5).
//
// The hooks run before the operation: in a project still on the lore 0.1.17
// format they keep lore's rules in the clone's own .git/info files. When
// `lore fix --migrate` raises the format inside the run, nothing wrote the
// tracked lines until the next lore command, so a second `lore fix` wrote
// .gitignore and .gitattributes and still printed "Everything is current."
// Now:
//
//	host hook writes a tracked file ─▶ HostWiring.Note
//	fix's migrate step raised the format ─▶ HostWiring.Rewire (an install
//	   step: the tracked lines are written now, in the same run)
//	fix ─▶ one "git wiring" step naming every file noted, with the commit
//	   line, so a run that wrote files never says everything is current

import (
	"context"
	"slices"
	"sync"
)

// HostWiring collects the tracked wiring files the host wrote during one
// operation. The host puts one on the operation's context
// (WithHostWiring); nil-safe.
type HostWiring struct {
	mu    sync.Mutex
	wrote []string
	// Rewire writes lore's tracked wiring of the project at root now
	// (.gitignore lines, the .gitattributes block) and returns the files
	// (relative to root) it changed. Set by the host; nil writes nothing.
	Rewire func(ctx context.Context, root string) []string
}

// Note records files (relative to the project root) the host wrote.
func (h *HostWiring) Note(files ...string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, f := range files {
		if !slices.Contains(h.wrote, f) {
			h.wrote = append(h.wrote, f)
		}
	}
}

// Wrote returns the files noted so far, in the order they were written.
func (h *HostWiring) Wrote() []string {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.wrote)
}

type hostWiringKey struct{}

// WithHostWiring returns ctx carrying h for the operation it runs.
func WithHostWiring(ctx context.Context, h *HostWiring) context.Context {
	return context.WithValue(ctx, hostWiringKey{}, h)
}

// HostWiringOf is the operation's HostWiring (nil when the host set none:
// a test, a REST call without the hooks).
func HostWiringOf(ctx context.Context) *HostWiring {
	h, _ := ctx.Value(hostWiringKey{}).(*HostWiring)
	return h
}
