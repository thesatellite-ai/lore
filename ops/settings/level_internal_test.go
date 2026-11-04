package settings

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// nameEnv is an Env with no rows that cannot say a store's level, so the
// name rule applies; levelEnv adds a Leveler answer per store.
type nameEnv struct{ stores []store.Source }

func (nameEnv) Create(context.Context, store.Source, row.Table, map[string]value.Value) (engine.Written, error) {
	return engine.Written{}, errs.New(errs.Internal, "nameEnv: no writes")
}

func (nameEnv) Update(context.Context, engine.Ref, map[string]value.Value) (engine.Written, error) {
	return engine.Written{}, errs.New(errs.Internal, "nameEnv: no writes")
}

func (nameEnv) Delete(context.Context, engine.Ref) (engine.Written, error) {
	return engine.Written{}, errs.New(errs.Internal, "nameEnv: no writes")
}

func (nameEnv) Get(context.Context, engine.Ref) (store.Source, row.Row, error) {
	return "", row.Row{}, errs.New(errs.NotFound, "nameEnv: no rows")
}

func (nameEnv) List(context.Context, store.Source, row.Table) ([]row.Row, error) { return nil, nil }

func (e nameEnv) Stores() []store.Source { return e.stores }

type levelEnv struct {
	nameEnv
	levels map[store.Source]policy.Level
}

func (e levelEnv) StoreLevel(src store.Source) (policy.Level, bool) {
	l, ok := e.levels[src]
	return l, ok
}

// TestStoreLevel: the engine's level (Leveler) wins over the name rule,
// which applies only to a store the Env does not know (or an empty
// answer). The org-attached linked folder `agency` is the case the name
// rule gets wrong (it reads as a store root).
func TestStoreLevel(t *testing.T) {
	byName := map[store.Source]policy.Level{
		store.SourceRepo: policy.LevelRepo, "me": policy.LevelPersonal, "kb/org": policy.LevelOrg,
		"kb/web": policy.LevelFolder, "kb/a/b": policy.LevelFolder, "kb": policy.LevelStoreRoot,
		"agency": policy.LevelStoreRoot, "": policy.LevelStoreRoot, "org": policy.LevelStoreRoot,
		"me/notes": policy.LevelFolder, "ünï/org": policy.LevelOrg,
	}
	for src, want := range byName {
		if got := storeLevel(nameEnv{}, src); got != want {
			t.Errorf("name rule %q: %s, want %s", src, got, want)
		}
	}
	env := levelEnv{levels: map[store.Source]policy.Level{"agency": policy.LevelFolder, "agency/org": policy.LevelOrg, "blank": ""}}
	for src, want := range map[store.Source]policy.Level{
		"agency": policy.LevelFolder, "agency/org": policy.LevelOrg,
		"blank": policy.LevelStoreRoot, "kb/web": policy.LevelFolder, store.SourceRepo: policy.LevelRepo,
	} {
		if got := storeLevel(env, src); got != want {
			t.Errorf("leveler %q: %s, want %s", src, got, want)
		}
	}
}

// TestScopeOf: the `scope` word of a settings row per place (spec 4.3).
func TestScopeOf(t *testing.T) {
	for src, want := range map[store.Source]string{
		"": "repo", store.SourceRepo: "repo", "kb": "store", "kb/web": "web", "kb/org": "org", "kb/a/b": "a/b", "me": "store",
	} {
		if got := scopeOf(src); got != want {
			t.Errorf("%q: %q, want %q", src, got, want)
		}
	}
}

// TestPolicySetRefusesPersonal: a personal store holds no team rules,
// refused before anything is read or written.
func TestPolicySetRefusesPersonal(t *testing.T) {
	o := ops{env: nameEnv{stores: []store.Source{store.SourceRepo, "me"}}}
	for _, to := range []string{"me"} {
		_, err := o.policySet(context.Background(), PolicySetIn{Key: string(policy.KeyOnDetect), Value: "refuse", To: to})
		if !errs.Has(err, errs.InvalidInput) {
			t.Errorf("--to %s: %v", to, err)
		}
	}
}
