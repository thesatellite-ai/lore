package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// orgStore is the name the org store is attached under in these tests
// (what `--to agency` names in a client repo).
const orgStore = "agency"

// storeIDs are arbitrary store ids for the folder stores of a test.
var storeIDs = []string{"sto_0e45506e64357c94a87e70ef1c6bbb01", "sto_0e45506e64357c94a87e70ef1c6bbb02"}

// newFolderStore makes an initialised internal folder store and returns
// its file:// url.
func newFolderStore(t *testing.T, i int) string {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.Parse[ids.Store](storeIDs[i])
	if err != nil {
		t.Fatal(err)
	}
	if err := folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	return fileuri.URL(dir, "")
}

// withOrg attaches an internal folder store as the org store (Options.
// OrgStores, the way a client repo reaches its org without a stores.yaml
// entry).
func withOrg(t *testing.T) func(*wire.Options) {
	t.Helper()
	url := newFolderStore(t, 0)
	return func(o *wire.Options) {
		o.OrgStores = func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: orgStore, URL: url}}, nil
		}
	}
}

// withPersonal gives the harness a personal store `me` (config.yaml
// personal.store in its own lore home).
func withPersonal(t *testing.T) func(*wire.Options) {
	t.Helper()
	url := newFolderStore(t, 1)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, wire.ConfigFile), []byte("personal:\n  store: "+url+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return func(o *wire.Options) { o.Home = home }
}

// repoClient makes the repo a client repo (true) or an internal one.
func repoClient(client bool) func(*wire.Options) {
	return func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) { return client, nil }
	}
}

// repoClientErr makes the client check fail (the repo must count as a
// client repo: fail closed).
func repoClientErr() func(*wire.Options) {
	return func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) {
			return false, errs.New(errs.StoreUnreachable, "links unreadable")
		}
	}
}

// runCtx runs an operation under ctx (the team flags read the host's
// work.Team from the context).
func runCtx(h *opstest.H, ctx context.Context, name, input string) (op.Result, error) {
	h.T.Helper()
	e, ok := h.App.Registry().Lookup(name)
	if !ok {
		h.T.Fatalf("no op %s", name)
	}
	return h.App.Run(ctx, e, op.JSONDecoder{Body: []byte(input)}, op.Caller{})
}

// mustCtx is runCtx failing the test on an error.
func mustCtx(h *opstest.H, ctx context.Context, name, input string) op.Result {
	h.T.Helper()
	res, err := runCtx(h, ctx, name, input)
	if err != nil {
		h.T.Fatalf("%s %s: %v", name, input, err)
	}
	return res
}

// addID is the id of a task.add output (inside its task.show envelope).
func addID(h *opstest.H, res op.Result) string {
	h.T.Helper()
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		h.T.Fatal(err)
	}
	if out.Data.ID == "" {
		h.T.Fatalf("no id in %s", h.JSON(res))
	}
	return out.Data.ID
}

// add adds a task with extra JSON fields ("" = none) and returns its id.
func add(h *opstest.H, title, extra string) string {
	h.T.Helper()
	in := `{"title":` + opstest.Quote(title)
	if extra != "" {
		in += "," + extra
	}
	return addID(h, h.Must(opstask.OpAdd, in+"}"))
}

// get reads a row and the store holding it.
func get(h *opstest.H, table row.Table, id string) (store.Source, row.Row) {
	h.T.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		h.T.Fatal(err)
	}
	src, r, err := h.App.Get(context.Background(), engine.Ref{Table: table, ID: aid})
	if err != nil {
		h.T.Fatalf("get %s: %v", id, err)
	}
	return src, r
}

// field is a row field's string form ("" when unset).
func field(r row.Row, name string) string {
	if v, ok := r.Fields[name]; ok && v != nil {
		return v.String()
	}
	return ""
}

// lines normalises Text() output for comparison with v0.1.17: ids become
// <prefix>_ID.
func lines(s string) string { return opstest.IDPattern(s) }

// create writes a row into the repo store straight through the engine (roster rows the ops
// read but do not write).
func create(h *opstest.H, table row.Table, fields map[string]value.Value) string {
	h.T.Helper()
	w, err := h.App.Create(context.Background(), store.SourceRepo, table, fields)
	if err != nil {
		h.T.Fatalf("create %s: %v", table, err)
	}
	return w.Row.ID.String()
}

// text is value.TextOf failing the test.
func text(t testing.TB, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// ref is value.RefOf of an id string, failing the test.
func ref(t testing.TB, id string) value.Value {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	v, err := value.RefOf(aid)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// newActor writes a human actor row and returns its id.
func newActor(h *opstest.H, name string) string {
	h.T.Helper()
	return create(h, schema.TableActors, map[string]value.Value{
		"kind": text(h.T, schema.ActorKindHuman), "display_name": text(h.T, name), "stable_key": text(h.T, "human:"+strings.ToLower(name)),
	})
}

// newTeam writes an active team row with members and returns its id.
func newTeam(h *opstest.H, handle string, members ...string) string {
	h.T.Helper()
	items := make([]value.Value, 0, len(members))
	for _, m := range members {
		items = append(items, ref(h.T, m))
	}
	list, err := value.ListOf(value.KindRef, items...)
	if err != nil {
		h.T.Fatal(err)
	}
	return create(h, schema.TableTeams, map[string]value.Value{
		"project_id": ref(h.T, h.Project), "handle": text(h.T, handle), schema.FieldTeamMembers: list, "active": value.BoolOf(true),
	})
}

// hintOf is the hint of the first *errs.Error in err's chain.
func hintOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}
