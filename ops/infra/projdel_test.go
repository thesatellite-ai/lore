package infra_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
)

// mintedCheckout is a checkout whose permanent repo id is minted (its own
// repos row written, PROV1), as after `lore init`.
func mintedCheckout(t *testing.T) (*opstest.H, string) {
	t.Helper()
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	rep, err := h.App.CheckRepoAddresses(context.Background())
	if err != nil || !rep.Minted {
		t.Fatalf("mint: %+v %v", rep, err)
	}
	return h, rep.RepoID
}

// deleted reports whether the row id of table is a tombstone.
func deleted(t *testing.T, h *opstest.H, table row.Table, raw string) bool {
	t.Helper()
	id, err := ids.ParseAny(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{Table: table, ID: id})
	if err != nil {
		t.Fatalf("get %s: %v", raw, err)
	}
	return r.Deleted
}

// TestProjectDeleteTakesOwnRepoRow pins Q-R3-11: with only the checkout's
// own repos row pointing at it, project delete tombstones that row and the
// project under one bat_ batch of kind delete.
func TestProjectDeleteTakesOwnRepoRow(t *testing.T) {
	h, own := mintedCheckout(t)
	h.Fails(infra.OpProjectDelete, `{"id":"`+h.Project+`"}`, errs.NeedsConfirm)
	if got := opstest.Text(h.MustYes(infra.OpProjectDelete, `{"id":"`+h.Project+`"}`)); got != "✓ demo deleted\n" {
		t.Errorf("delete text %q", got)
	}
	if !deleted(t, h, schema.TableProjects, h.Project) || !deleted(t, h, schema.TableRepos, own) {
		t.Fatal("project and own repos row must both be tombstones")
	}
	evs, err := h.App.BatchEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	batches := map[string][]string{}
	for _, ev := range evs {
		if ev.Kind != "delete" || ev.Op != infra.OpProjectDelete {
			t.Fatalf("event %+v", ev)
		}
		batches[ev.Batch.String()] = append(batches[ev.Batch.String()], ev.ID.String())
	}
	if len(batches) != 1 {
		t.Fatalf("want one batch, got %v", batches)
	}
	for b, rows := range batches {
		if !strings.HasPrefix(b, "bat_") || len(rows) != 2 {
			t.Fatalf("batch %s rows %v", b, rows)
		}
	}
}

// TestProjectDeleteOtherRepoRefuses: another repos row of the project
// (DELETION D8) refuses with E_HAS_DEPENDENTS naming it, and nothing is
// deleted, the own row included.
func TestProjectDeleteOtherRepoRefuses(t *testing.T) {
	h, own := mintedCheckout(t)
	other := h.ID(h.Must(infra.OpRepoAdd, `{"mount_name":"web"}`))
	_, err := h.RunYes(infra.OpProjectDelete, `{"id":"`+h.Project+`"}`)
	if !errs.Has(err, errs.HasDependents) {
		t.Fatalf("want E_HAS_DEPENDENTS, got %v", err)
	}
	msg := errText(err)
	if !strings.Contains(msg, other+" (repos.project_id) still points at "+h.Project) || strings.Contains(msg, own) {
		t.Errorf("message must name only the other repo: %q", msg)
	}
	if deleted(t, h, schema.TableProjects, h.Project) || deleted(t, h, schema.TableRepos, own) || deleted(t, h, schema.TableRepos, other) {
		t.Fatal("a refused delete must write nothing")
	}
}

// TestProjectDeleteWithoutIdentityRefuses: a checkout with no repo id
// keeps every repos row a dependent (fail closed, the refusal before
// Q-R3-11).
func TestProjectDeleteWithoutIdentityRefuses(t *testing.T) {
	h := opstest.New(t)
	rep := h.ID(h.Must(infra.OpRepoAdd, `{"mount_name":"api"}`))
	_, err := h.RunYes(infra.OpProjectDelete, `{"id":"`+h.Project+`"}`)
	if !errs.Has(err, errs.HasDependents) || !strings.Contains(errText(err), rep) {
		t.Fatalf("want E_HAS_DEPENDENTS naming %s, got %v", rep, err)
	}
	if deleted(t, h, schema.TableProjects, h.Project) {
		t.Fatal("project deleted")
	}
}
