package settings

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// freshEnv answers StoreFresh per store, the policy of every target and
// the clock; it holds no rows.
type freshEnv struct {
	nameEnv
	now    time.Time
	fresh  map[store.Source]store.Fresh
	failOn store.Source
	pol    policy.Set
	asked  []store.Source
}

func (e *freshEnv) Now() time.Time { return e.now }

func (e *freshEnv) StoreFresh(_ context.Context, src store.Source) (store.Fresh, error) {
	e.asked = append(e.asked, src)
	if src == e.failOn {
		return store.Fresh{}, errs.New(errs.Internal, "stamp unreadable")
	}
	return e.fresh[src], nil
}

func (e *freshEnv) Policy(context.Context, store.Source) (policy.Set, error) { return e.pol, nil }

// staleAfter is a policy Set with resources.stale_after = h at the repo.
func staleAfter(t *testing.T, h int64) policy.Set {
	t.Helper()
	src, err := ids.ParseAny("pol_01936f3400007e5f8a6b7c8d9e0f1a21")
	if err != nil {
		t.Fatal(err)
	}
	s, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: policy.KeyResourcesStaleAfter, Value: value.Int(h), Source: src}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestStaleNote (RES 3.6, P2-RES-6..8): a resource write checked against a
// store's kind rows notes "checked against store kinds as of <commit>"
// only when that store's last fetch is older than resources.stale_after
// (the write target's policy; default 24 h); a fresh stamp, an exact
// threshold, a store with no stamp (folder, repo) or an unreadable stamp
// add nothing; only the store holding the kind row is asked; the note
// never refuses.
func TestStaleNote(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	const commit = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name  string
		fr    store.Fresh
		pol   policy.Set
		fail  bool
		want  string // "" = no note
		short string
	}{
		{"fresh (1 h)", store.Fresh{Known: true, FetchedAt: now.Add(-time.Hour), Commit: commit}, policy.Defaults(), false, "", ""},
		{"exactly 24 h", store.Fresh{Known: true, FetchedAt: now.Add(-24 * time.Hour), Commit: commit}, policy.Defaults(), false, "", ""},
		{"25 h, default 24", store.Fresh{Known: true, FetchedAt: now.Add(-25 * time.Hour), Commit: commit}, policy.Defaults(), false, "checked against store kinds as of 0123456789ab", "2026-11-04"},
		{"2 h past a 1 h policy", store.Fresh{Known: true, FetchedAt: now.Add(-2 * time.Hour), Commit: commit}, staleAfter(t, 1), false, "checked against store kinds as of", ""},
		{"25 h under a 48 h policy", store.Fresh{Known: true, FetchedAt: now.Add(-25 * time.Hour), Commit: commit}, staleAfter(t, 48), false, "", ""},
		{"stale, no commit", store.Fresh{Known: true, FetchedAt: now.Add(-100 * time.Hour)}, policy.Defaults(), false, "as of an unknown commit", ""},
		{"short commit kept", store.Fresh{Known: true, FetchedAt: now.Add(-100 * time.Hour), Commit: "abc"}, policy.Defaults(), false, "as of abc (kb", ""},
		{"no stamp (folder/repo store)", store.Fresh{}, policy.Defaults(), false, "", ""},
		{"stamp unreadable", store.Fresh{}, policy.Defaults(), true, "", ""},
	}
	for _, c := range cases {
		env := &freshEnv{now: now, fresh: map[store.Source]store.Fresh{"kb": c.fr, store.SourceRepo: {Known: true, FetchedAt: now.Add(-1000 * time.Hour)}}, pol: c.pol}
		if c.fail {
			env.failOn = "kb"
		}
		o := ops{env: env}
		d := kindDef{kind: located{Src: "kb", Row: row.Row{Table: schema.TableKinds}}}
		got := o.staleNote(context.Background(), d, store.SourceRepo)
		if c.want == "" && got != "" {
			t.Errorf("%s: unexpected note %q", c.name, got)
		}
		if c.want != "" && (!strings.Contains(got, c.want) || !strings.Contains(got, "run lore sync") || !strings.Contains(got, c.short)) {
			t.Errorf("%s: note %q, want %q", c.name, got, c.want)
		}
		if len(env.asked) != 1 || env.asked[0] != "kb" {
			t.Errorf("%s: asked %v, want only the kind's store kb", c.name, env.asked)
		}
	}
	// No freshness upgrade at all: nothing.
	if got := (ops{env: nameEnv{}}).staleNote(context.Background(), kindDef{kind: located{Src: "kb"}}, store.SourceRepo); got != "" {
		t.Errorf("env without StoreFresh: %q", got)
	}
}
