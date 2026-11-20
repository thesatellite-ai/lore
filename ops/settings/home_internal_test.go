package settings

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// homeEnv is a settings Env of a linked repo: it records where each
// create lands and answers the repoHomer, projecter and FileAllower
// upgrades the way wire.App does there. The repo store holds no rows.
type homeEnv struct {
	home    store.Source
	homeErr error
	project ids.ID[ids.Project]
	created []store.Source
}

func (e *homeEnv) Create(_ context.Context, target store.Source, _ row.Table, fields map[string]value.Value) (engine.Written, error) {
	e.created = append(e.created, target)
	id, err := ids.New[ids.SecretAllow]()
	if err != nil {
		return engine.Written{}, err
	}
	return engine.Written{Row: row.Row{ID: id.Any(), Fields: fields}}, nil
}

func (e *homeEnv) Update(context.Context, engine.Ref, map[string]value.Value) (engine.Written, error) {
	return engine.Written{}, errs.New(errs.Internal, "homeEnv: no update")
}

func (e *homeEnv) Delete(context.Context, engine.Ref) (engine.Written, error) {
	return engine.Written{}, errs.New(errs.Internal, "homeEnv: no delete")
}

func (e *homeEnv) Get(context.Context, engine.Ref) (store.Source, row.Row, error) {
	return "", row.Row{}, errs.New(errs.NotFound, "homeEnv: no rows")
}

func (e *homeEnv) List(context.Context, store.Source, row.Table) ([]row.Row, error) { return nil, nil }

func (e *homeEnv) Stores() []store.Source { return []store.Source{store.SourceRepo, e.home} }

func (e *homeEnv) RepoHomeStore(context.Context) (store.Source, bool, error) {
	return e.home, true, e.homeErr
}

func (e *homeEnv) Project(context.Context) (ids.ID[ids.Project], error) { return e.project, nil }

func (e *homeEnv) RepoFilePath(_ context.Context, p string) (string, error) { return p, nil }

func (e *homeEnv) FileFindingHash(context.Context, string, string, string) (uint32, error) {
	return 42, nil
}

// TestLinkedRepoSettingsWritesGoToTheFolder (devco D-736, the secrets
// allow class): in a linked repo whose .lore/data holds no rows, `lore
// secrets allow --file` refused with "this repo has no lore project" (it
// listed the repo store's project rows) and wrote its entry into the repo;
// every settings write whose default is "this repo" now lands in the
// linked folder, and a folder not open here refuses instead of writing to
// the repo.
func TestLinkedRepoSettingsWritesGoToTheFolder(t *testing.T) {
	ctx := context.Background()
	pid, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	env := &homeEnv{home: "acme/billing", project: pid}
	o := ops{env: env}

	out, err := o.allow(ctx, AllowIn{File: "config.yaml", Rule: "generic-password", Reason: "a fixture", MatchHash: "sha256:0123456789abcdef"})
	if err != nil {
		t.Fatalf("secrets allow --file in a linked repo: %v", err)
	}
	if out.Store != "acme/billing" || len(env.created) != 1 || env.created[0] != "acme/billing" {
		t.Fatalf("the allow entry went to %v (out.Store %q), want the linked folder acme/billing", env.created, out.Store)
	}

	env.created = nil
	if _, err = o.ruleOff(ctx, RuleOffIn{RuleID: "generic-password", Reason: "too noisy"}); err != nil {
		t.Fatalf("secrets rule off in a linked repo: %v", err)
	}
	if len(env.created) != 1 || env.created[0] != "acme/billing" {
		t.Fatalf("secrets rule off wrote to %v, want the linked folder", env.created)
	}

	// --to repo still names the repo store
	if got, herr := o.home(ctx, string(store.SourceRepo)); herr != nil || got != store.SourceRepo {
		t.Fatalf("home(--to repo) = %q, %v", got, herr)
	}

	// a linked folder not open here: refused, nothing written to the repo
	env.created, env.homeErr = nil, errs.New(errs.StoreUnreachable, "not attached")
	_, err = o.allow(ctx, AllowIn{File: "config.yaml", Rule: "generic-password", Reason: "a fixture", MatchHash: "sha256:0123456789abcdef"})
	if errs.CodeOf(err) != errs.StoreUnreachable || len(env.created) != 0 {
		t.Fatalf("folder not open: err %v, wrote %v; want E_STORE_UNREACHABLE and no write", err, env.created)
	}
}
