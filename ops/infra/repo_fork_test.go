package infra_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
)

// rowOf reads one row (live or tombstone) of the repo store.
func rowOf(t *testing.T, h *opstest.H, table row.Table, raw string) (project, origRepo string, gone bool) {
	t.Helper()
	id, err := ids.ParseAny(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{Table: table, ID: id})
	if err != nil {
		t.Fatalf("get %s: %v", raw, err)
	}
	var o struct {
		Repo string `json:"repo"`
	}
	if !r.Origin.IsZero() {
		if jerr := json.Unmarshal(r.Origin.Raw(), &o); jerr != nil {
			t.Fatalf("_o of %s: %v", raw, jerr)
		}
	}
	if v, ok := r.Fields["project_id"]; ok && v != nil {
		project = v.String()
	}
	return project, o.Repo, r.Deleted
}

// metaProjectID reads project_id from the checkout's _meta.json (RepoMeta
// carries only the identity keys).
func metaProjectID(t *testing.T, h *opstest.H) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.Repo, rowfs.LoreDir, rowfs.DataDir, rowfs.MetaFileName))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m.ProjectID
}

// TestForkNewProject pins FA-AGENT-10 (PROVENANCE 10.6): `repo fork
// --new-project` writes a new projects row and _meta.json project_id,
// re-points the rows made before the fork to it while their _o.repo still
// names the upstream, tombstones the upstream project in this fork, and a
// row made after the fork takes the new project and the fork's repo id.
// When the upstream project row comes back live (a merge from upstream
// would bring it), Project() follows _meta.json, not the oldest row.
func TestForkNewProject(t *testing.T) {
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	ctx := context.Background()
	up, err := h.App.CheckRepoAddresses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := h.ID(h.Must("memory.add", `{"body":"made before the fork"}`))
	if p, o, _ := rowOf(t, h, schema.TableMemories, before); p != h.Project || o != up.RepoID {
		t.Fatalf("before the fork: project %s _o.repo %s", p, o)
	}
	setOrigin(t, h.Repo, "https://github.com/tomas-k/acmehub_backend.git")
	if _, cerr := h.App.CheckRepoAddresses(ctx); cerr != nil {
		t.Fatal(cerr)
	}
	res := h.Must(infra.OpRepoFork, `{"new_project":true}`)
	np, upP, fork := h.Field(res, "project_id"), h.Field(res, "upstream_project_id"), h.Field(res, "repo_id")
	if np == "" || np == h.Project || upP != h.Project || fork == up.RepoID {
		t.Fatalf("fork result: %s", h.JSON(res))
	}
	m := meta(t, h)
	if metaProjectID(t, h) != np || m.RepoID != fork || m.ForkedFrom != up.RepoID {
		t.Fatalf("_meta.json: %+v", m)
	}
	if p, o, gone := rowOf(t, h, schema.TableMemories, before); p != np || o != up.RepoID || gone {
		t.Fatalf("pre-fork row: project %s (want %s) _o.repo %s (want upstream %s) deleted %v", p, np, o, up.RepoID, gone)
	}
	if _, _, gone := rowOf(t, h, schema.TableProjects, h.Project); !gone {
		t.Fatal("the upstream project must be a tombstone in the fork")
	}
	if _, _, gone := rowOf(t, h, schema.TableProjects, np); gone {
		t.Fatal("the new project is not live")
	}
	pid, err := h.App.Project(ctx)
	if err != nil || pid.String() != np {
		t.Fatalf("Project() = %s %v, want %s", pid, err, np)
	}
	after := h.ID(h.Must("memory.add", `{"body":"made in the fork"}`))
	if p, o, _ := rowOf(t, h, schema.TableMemories, after); p != np || o != fork {
		t.Fatalf("post-fork row: project %s _o.repo %s, want %s %s", p, o, np, fork)
	}
	// The upstream project row comes back live: two live projects, and
	// _meta.json's project_id picks this checkout's own.
	h.MustYes("trash.restore", `{"target":"`+h.Project+`"}`)
	if pid, err = h.App.Project(ctx); err != nil || pid.String() != np {
		t.Fatalf("two live projects: Project() = %s %v, want %s from _meta.json", pid, err, np)
	}
}
