package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// overlayWrap is a repo-store wrapper that may serve the live ref
// (wire.LiveOverlay), as cmd/lore's wire/host liveBackend does.
type overlayWrap struct {
	store.Backend
	serves bool
}

func (o overlayWrap) Unwrap() store.Backend { return o.Backend }
func (o overlayWrap) ServesLiveRef() bool   { return o.serves }

// plainWrap is a wrapper that is no overlay (another host's wrapper).
type plainWrap struct{ store.Backend }

func (p plainWrap) Unwrap() store.Backend { return p.Backend }

// TestLiveNoteOverlay: the v0.1.17 "live rows not visible yet; run lore
// migrate run" note is printed for a repo whose live ref holds rows the
// core cannot see, unless the repo store is served by a live overlay
// (cmd/lore's live backend, found through any chain of Unwrappers): there
// the live rows are 0.1.18 live rows that a run without a live session
// (a git hook, task heartbeat) only misses for itself, so no note. An
// overlay that says it does not serve the live ref keeps the note.
func TestLiveNoteOverlay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		wrap func(store.Backend) store.Backend
		note bool
	}{
		{"no wrapper", nil, true},
		{"other wrapper", func(b store.Backend) store.Backend { return plainWrap{b} }, true},
		{"overlay not serving", func(b store.Backend) store.Backend { return overlayWrap{Backend: b} }, true},
		{"overlay", func(b store.Backend) store.Backend { return overlayWrap{Backend: b, serves: true} }, false},
		{"overlay under another wrapper", func(b store.Backend) store.Backend {
			return plainWrap{overlayWrap{Backend: plainWrap{b}, serves: true}}
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			repo := gitRepo(t)
			opts := Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }}
			if c.wrap != nil {
				opts.WrapBackend = func(_ context.Context, name store.Source, _ string, b store.Backend) store.Backend {
					if name != store.SourceRepo {
						return b
					}
					return c.wrap(b)
				}
			}
			a := openWith(t, repo, opts)
			withProject(t, a)
			writeLiveRef(t, repo, liveRefLocal, map[string]string{
				livePath(liveOnlyMem): liveMemory(liveOnlyMem, projectID(t, a), "live only", liveNewer),
			})
			note := a.LiveNote(background())
			if got := strings.Contains(note, MigrateCommand); got != c.note {
				t.Fatalf("note %q, want a note: %v", note, c.note)
			}
		})
	}
}
