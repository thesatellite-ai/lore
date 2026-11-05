package settings_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// freshBackend is a store backend with a fixed fetch stamp (the fake
// store.Freshness of a git store fetched `age` ago, or never: known
// false). Unwrap keeps the inner backend's capabilities reachable.
type freshBackend struct {
	store.Backend
	known bool
	at    time.Time
}

func (f freshBackend) Fresh(context.Context) (store.Fresh, error) {
	return store.Fresh{Known: f.known, FetchedAt: f.at}, nil
}

func (f freshBackend) Unwrap() store.Backend { return f.Backend }

// withFresh wraps every attached store (never the repo store) with a
// stamp fetched age ago (known false: never fetched), and sets the repo's
// own/client answer.
func withFresh(known bool, age time.Duration, client bool) func(*wire.Options) {
	return func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) { return client, nil }
		o.WrapBackend = func(_ context.Context, name store.Source, _ string, b store.Backend) store.Backend {
			if name == store.SourceRepo {
				return b
			}
			return freshBackend{Backend: b, known: known, at: time.Now().Add(-age)}
		}
	}
}

// TestMetaStale (METADATA 3.9/3.18, P2-META-14): auto-registering a NEW
// key in an own repo is refused E_META_STALE when an attached store was
// last fetched longer ago than meta.stale_after (default 168 h); a store
// never fetched (Known false), a client repo, a key scoped to a place
// (repo:key=v), a key that already exists, and meta.stale_after 0 all
// pass. The refusal writes nothing.
func TestMetaStale(t *testing.T) {
	const stale, fresh = 200 * time.Hour, 100 * time.Hour
	cases := []struct {
		name     string
		known    bool
		age      time.Duration
		client   bool
		policy   string // meta.stale_after set in the repo ("" = default 168)
		meta     string // the --meta entry of the write
		existing bool   // register the key first
		refused  bool
	}{
		{name: "stale default refuses new key", known: true, age: stale, meta: "jira=HUB-1", refused: true},
		{name: "fresh default passes", known: true, age: fresh, meta: "jira=HUB-1"},
		{name: "never fetched passes", known: false, age: stale, meta: "jira=HUB-1"},
		{name: "client repo passes", known: true, age: stale, client: true, meta: "jira=HUB-1"},
		{name: "scoped repo key passes", known: true, age: stale, meta: "repo:jira=HUB-1"},
		{name: "existing key passes", known: true, age: stale, meta: "jira=HUB-1", existing: true},
		{name: "stale_after 0 disables", known: true, age: stale, policy: "0", meta: "jira=HUB-1"},
		{name: "stale_after 300 passes 200 h", known: true, age: stale, policy: "300", meta: "jira=HUB-1"},
		{name: "stale_after 150 refuses 200 h", known: true, age: stale, policy: "150", meta: "jira=HUB-1", refused: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := multiStore(t, withFresh(c.known, c.age, c.client))
			if c.policy != "" {
				h.MustYes(settings.OpPolicySet, setIn("meta.stale_after", c.policy, "repo"))
			}
			if c.existing {
				h.MustYes(settings.OpMetaAdd, `{"name":"jira","type":"text","to":"repo"}`)
			}
			in := `{"body":"stale check","meta":[` + opstest.Quote(c.meta) + `]}`
			if !c.refused {
				h.MustYes(memAdd, in)
				return
			}
			msg := h.Fails(memAdd, in, errs.MetaStale)
			if !strings.Contains(msg, "jira") || !strings.Contains(msg, "last sync 8 days ago") {
				t.Errorf("message %q", msg)
			}
			if list := opstest.Text(h.Must("memory.list", `{}`)); strings.Contains(list, "stale check") {
				t.Errorf("a refused write left a row: %q", list)
			}
			if keys := opstest.Text(h.Must(settings.OpMetaKeys, `{}`)); strings.Contains(keys, "jira") {
				t.Errorf("a refused write registered the key: %q", keys)
			}
		})
	}
}
