package app

// apirunner.go — the runner `lore api` serves REST requests through: the
// CLI's own host runner (wire/host.Runner with every hook), never the bare
// App, as v0.1.17's api_runner.go did. Without the hooks a request skipped
// the live hook, and with it F16 on the branch rows of a client repo
// (wire/host leakCheckBranch): an edit, or a create of a table the
// engine's repo-write check does not route (a hotfix), wrote internal text
// into the client's .lore/data over REST while the CLI refused it
// (teame2e TestF16_EveryRepoWritePathIsChecked).
//
// Requests run one at a time: the live hook keeps one session per
// operation and the runner's State is per process, as in a CLI process;
// each request then ends its live session (push, land, release the lock)
// like the end of a CLI command. Each request's context is marked
// wire.RemoteRequest, so the audit never links its writes to the server
// process's own (terminal) session row (OV3).

import (
	"context"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/surface/rest"
	"github.com/thesatellite-ai/lore/wire"
)

// apiRunner serves REST requests through inner, one at a time.
type apiRunner struct {
	inner op.Runner
	// join is the join the page serves (`lore setup join`), zero
	// otherwise: setup.list then offers that member setup (B-10).
	join opssetup.JoinContext
	mu   sync.Mutex
}

var _ op.Runner = (*apiRunner)(nil)

// Run implements op.Runner.
func (r *apiRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// OV3: the request's writes carry no session of the server process
	return r.inner.Run(opssetup.WithJoin(wire.RemoteRequest(ctx), r.join), e, d, c)
}

// apiKinds is the rest.KindSource of `lore api` (SPEC 9.6, CLI_REFERENCE
// 3.23: the served /openapi.json and /v1/spec carry one schema per kind of
// the attached stores). It reads the kinds and kind_fields rows of every
// attached store on each call, so a kind added while the API runs shows
// up on the next request, and builds each with schema.KindDefFromRows,
// the same run-time definition the cache and validator use. Stores are
// read in precedence order and the first definition of a kind name wins;
// a kind whose rows do not make a definition is left out (the cache skips
// it the same way).
type apiKinds struct{ app *wire.App }

var _ rest.KindSource = apiKinds{}

// Kinds implements rest.KindSource.
func (k apiKinds) Kinds(ctx context.Context) ([]rest.Kind, error) {
	eng, err := k.app.Engine(ctx)
	if err != nil {
		return nil, err
	}
	var out []rest.Kind
	seen := map[row.Table]bool{}
	for _, src := range eng.ReadStores() {
		kinds, err := eng.List(ctx, src, schema.TableKinds)
		if err != nil {
			return nil, err
		}
		if len(kinds) == 0 {
			continue
		}
		fields, err := eng.List(ctx, src, schema.TableKindFields)
		if err != nil {
			return nil, err
		}
		byKind := map[string][]row.Row{}
		for _, f := range fields {
			if v, ok := f.Fields[kindFieldKindID]; ok {
				byKind[v.String()] = append(byKind[v.String()], f)
			}
		}
		for _, kr := range kinds {
			def, derr := schema.KindDefFromRows(kr, byKind[kr.ID.String()])
			if derr != nil {
				continue
			}
			// The surfaces name a kind by its kind name ("host": the
			// /v1/resources/host route and the kind.host schema), not by
			// its cache table ("res_host").
			name, perr := row.ParseTable(strings.TrimPrefix(string(def.Name), schema.KindTablePrefix))
			if perr != nil || seen[name] {
				continue
			}
			seen[name] = true
			def.Name = name
			out = append(out, rest.Kind{Def: def, Source: string(src)})
		}
	}
	return out, nil
}

// kindFieldKindID is the kind_fields field naming its kind (schema
// builtin: Ref("kind_id", TableKinds)).
const kindFieldKindID = "kind_id"
