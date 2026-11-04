package pattern_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/ops/pattern"
	"github.com/thesatellite-ai/lore/wire"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's pattern facts (read from its real output).
var spec = contenttest.Spec{
	Noun: "pattern", Table: schema.TablePatterns, Prefix: "pat",
	KindList: "pattern.list", KindShow: "pattern.show", KindSearch: "pattern.search",
	Empty: "(no patterns)\n", Badge: "PA", TitleKey: "title",
	Add: func(title, body string) string {
		return `{"title":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "trust_score", "source_kind", "project_id", "title", "body", "created_by_actor_id"},
	HitKeys:    []string{"body", "title"},
	EditWord:   "pattern",
	EditTitle: func(id, v string) string {
		return `{"id":"` + id + `","title":` + contenttest.Quote(v) + `}`
	},
	Archive: true,
}

func TestPatternConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}

// failingEnv is the real App with the old row's supersede mark failing,
// so the rollback path of --supersedes runs.
type failingEnv struct {
	*wire.App
	failOn  string
	deleted []string
}

func (f *failingEnv) Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error) {
	if ref.ID.String() == f.failOn {
		return engine.Written{}, errs.New(errs.StoreLocked, "injected: store locked")
	}
	return f.App.Update(ctx, ref, patch)
}

func (f *failingEnv) Delete(ctx context.Context, ref engine.Ref) (engine.Written, error) {
	f.deleted = append(f.deleted, ref.ID.String())
	return f.App.Delete(ctx, ref)
}

// runWith runs pattern op name with env as the pattern Env (the App's
// Run path: scan, policy, write).
func runWith(t *testing.T, h *opstest.H, env content.Env, name, input string) (op.Result, error) {
	t.Helper()
	for _, e := range pattern.Ops(env) {
		if e.Spec().Name == name {
			actor, err := ids.Parse[ids.Actor](opstest.Actor)
			if err != nil {
				t.Fatal(err)
			}
			return h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(input)}, op.Caller{Actor: actor})
		}
	}
	t.Fatalf("no op %s", name)
	return op.Result{}, nil
}

// TestSupersedes: --supersedes marks the old pattern with the new id;
// an unknown, foreign or already-replaced old id is refused before
// anything is written; a failed mark deletes the new pattern again.
func TestSupersedes(t *testing.T) {
	h := opstest.New(t)
	old := h.ID(h.Must("pattern.add", `{"title":"v1","body":"first"}`))
	next := h.ID(h.Must("pattern.add", `{"title":"v2","body":"second","supersedes":"`+old+`"}`))
	if got := contenttest.Str(t, h.JSON(h.Must("pattern.show", `{"id":"`+old+`"}`)), "data", "superseded_by_id"); got != next {
		t.Fatalf("old superseded_by_id %q, want %s", got, next)
	}
	before := h.JSON(h.Must("pattern.list", ""))
	for name, c := range map[string]struct {
		sup  string
		code errs.Code
	}{
		"already replaced": {old, errs.InvalidInput},
		"unknown":          {"pat_01a1a5aca7f57659be7320c5f4b7ffff", errs.NotFound},
		// v0.1.17 parity: --supersedes answers E_NOT_FOUND for any value
		// that names no live pattern, malformed ones included.
		"wrong prefix": {"rul_01a1a5aca7f57659be7320c5f4b7ffff", errs.NotFound},
		"bad hex":      {"pat_zz", errs.NotFound},
		"garbage":      {"nope", errs.NotFound},
	} {
		msg := h.Fails("pattern.add", `{"title":"v3","body":"third","supersedes":"`+c.sup+`"}`, c.code)
		if name == "already replaced" && !strings.Contains(msg, next) {
			t.Errorf("already replaced: %q should name %s", msg, next)
		}
	}
	if after := h.JSON(h.Must("pattern.list", "")); after != before {
		t.Errorf("a refused --supersedes wrote a row:\n%s\n%s", before, after)
	}

	// a pattern of another project (same store) is not this project's
	h2 := opstest.New(t)
	name, err := value.TextOf("second-project")
	if err != nil {
		t.Fatal(err)
	}
	prj, err := h2.App.Create(context.Background(), "", schema.TableProjects, map[string]value.Value{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	other := h2.ID(h2.Must("pattern.add", `{"title":"other","body":"o","project":"`+prj.Row.ID.String()+`"}`))
	h2.Fails("pattern.add", `{"title":"v3","body":"third","project":"`+h2.Project+`","supersedes":"`+other+`"}`, errs.NotFound)

	// rollback: the mark fails, the new pattern is deleted again
	f := &failingEnv{App: h.App, failOn: next}
	_, err = runWith(t, h, f, "pattern.add", `{"title":"v3","body":"third","supersedes":"`+next+`"}`)
	msg := contenttest.Fails(t, "failed mark", err, errs.StoreLocked)
	if !strings.Contains(msg, "nothing was kept") {
		t.Errorf("failed mark message %q", msg)
	}
	if len(f.deleted) != 1 {
		t.Fatalf("rollback deleted %v, want the new pattern", f.deleted)
	}
	if _, err := h.Run("pattern.show", `{"id":"`+f.deleted[0]+`"}`); !errs.Has(err, errs.InTrash) {
		t.Errorf("the rolled-back pattern is still live: %v", err)
	}
	if got := contenttest.Str(t, h.JSON(h.Must("pattern.show", `{"id":"`+next+`"}`)), "data", "superseded_by_id"); got != "" {
		t.Errorf("old pattern marked %q although the mark failed", got)
	}
}

// TestLanguageAndRebind: --language (new) round-trips and edits; edit
// --rebind-master clears repo_id; --rebind-repo with an unknown repo is
// refused; both rebind flags at once are refused.
func TestLanguageAndRebind(t *testing.T) {
	h := opstest.New(t)
	id := h.ID(h.Must("pattern.add", `{"title":"t","body":"b","language":"go"}`))
	show := h.JSON(h.Must("pattern.show", `{"id":"`+id+`"}`))
	if contenttest.Str(t, show, "data", "language") != "go" {
		t.Errorf("language: %s", show)
	}
	h.Must("pattern.edit", `{"id":"`+id+`","language":"ts"}`)
	if got := contenttest.Str(t, h.JSON(h.Must("pattern.show", `{"id":"`+id+`"}`)), "data", "language"); got != "ts" {
		t.Errorf("edited language %q", got)
	}
	h.Fails("pattern.edit", `{"id":"`+id+`","rebind_repo":"web","rebind_master":true}`, errs.InvalidInput)
	h.Fails("pattern.edit", `{"id":"`+id+`","rebind_repo":"no-such-repo"}`, errs.NotFound)
	h.Fails("pattern.add", `{"title":"t2","body":"b","repo":"no-such-repo"}`, errs.NotFound)
	res := h.Must("pattern.edit", `{"id":"`+id+`","rebind_master":true}`)
	if got := opstest.Text(res); got != "✓ pattern "+id+" updated\n" {
		t.Errorf("rebind text %q", got)
	}
}

// v1Row is a pattern row file exactly as v0.1.17 wrote it (`_v: 1`,
// explicit nulls), with the project id left as %s.
const v1Row = `{
  "_table": "patterns",
  "_v": 1,
  "archived_at": "2026-11-04T12:19:47.187248009Z",
  "body": "B retry",
  "confidence": null,
  "created_at": "2026-11-04T12:14:38.770802455Z",
  "created_by_actor_id": "act_890d10959a8a78588031ef09add2af4a",
  "id": "pat_01a1a67b92b27d88b9ae07e8d9a0a885",
  "language": null,
  "last_validated_at": null,
  "project_id": "%s",
  "repo_id": null,
  "source_kind": "manual",
  "source_ref": null,
  "superseded_by_id": null,
  "title": "T q",
  "trust_score": 0.5,
  "updated_at": "2026-11-04T12:19:47.187267882Z"
}
`

// v1Show is v0.1.17's `pattern show --json` of v1Row (compacted), the
// project id left as %s.
const v1Show = `{"schema_version":1,"kind":"pattern.show","data":{"id":"pat_01a1a67b92b27d88b9ae07e8d9a0a885","created_at":"2026-11-04T12:14:38.770802455Z","updated_at":"2026-11-04T12:19:47.187267882Z","trust_score":0.5,"archived_at":"2026-11-04T12:19:47.187248009Z","source_kind":"manual","project_id":"%s","title":"T q","body":"B retry","created_by_actor_id":"act_890d10959a8a78588031ef09add2af4a"}}`

// TestV1RowReads: a v0.1.17 row file is read through the core with the
// same --json bytes v0.1.17 printed for it (nulls dropped, key order,
// nanosecond times kept), and can be edited.
func TestV1RowReads(t *testing.T) {
	h := opstest.New(t)
	dir := filepath.Join(h.Repo, ".lore", "data", "patterns")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	id := "pat_01a1a67b92b27d88b9ae07e8d9a0a885"
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(fmt.Sprintf(v1Row, h.Project)), 0o600); err != nil {
		t.Fatal(err)
	}
	got := h.JSON(h.Must("pattern.show", `{"id":"`+id+`"}`))
	if want := fmt.Sprintf(v1Show, h.Project); got != want {
		t.Errorf("v1 row --json\n%s\nwant v0.1.17's\n%s", got, want)
	}
	if !strings.Contains(opstest.Text(h.Must("pattern.list", "")), "PA  "+id) {
		t.Error("list misses the v1 row")
	}
	h.Must("pattern.edit", `{"id":"`+id+`","title":"edited"}`)
	if got := contenttest.Str(t, h.JSON(h.Must("pattern.show", `{"id":"`+id+`"}`)), "data", "title"); got != "edited" {
		t.Errorf("edit of a v1 row: title %q", got)
	}
}
