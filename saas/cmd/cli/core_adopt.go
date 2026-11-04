// core_adopt.go — a project adoption (E1) or a `lore sync fix-projects`
// fold carried into the core-owned row files.
//
// When a clone that ran without sync turns it on, lsync's ADOPT pass
// replaces the clone's own project id by the shared one _meta.json pins,
// so its rows hang off the team's project (v0.1.17 parity). It can only do
// that in lore.db: the row files of core-owned tables (memories, rules,
// tasks, projects, repos, …) are written by the 0.1.18 core alone
// (lsync/owned.go), so they kept the local project id and the next pass
// imported it straight back. lsync leaves a durable mark per adoption
// (lsync.PendingProjectAdoptions); reconcileCheckout re-points every
// core-owned row that names the local project through the core engine
// (Update: HLC stamp, audit, secret scan and merge rules all hold), then
// deletes the local project's row through the engine (a tombstone, DQ4, so
// an old copy can never bring it back), clears the mark, and runs one more
// pass so lore.db mirrors the rewritten files. `lore sync fix-projects`
// (lsync.FixProjects) leaves the same mark for every project it folds and
// then runs a pass, so it takes this path too. Idempotent: a re-run finds
// no row naming the local id and an already deleted project.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"

	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/style"
)

// adoptFailed holds the checkout roots whose core re-pointing failed in
// this process, so a command's start and end passes do not both retry it
// and print the same warning twice. Per process, like syncSessions: the
// next command retries.
var adoptFailed = map[string]bool{}

// reconcileCheckout is lsync.Reconcile for the checkout's own .lore/data
// plus the core half of a project adoption (see the file comment). A
// failure of the core half is printed, keeps the mark for the next
// command, and never fails the pass that already committed.
func reconcileCheckout(ctx context.Context, o lsync.Options, root string) (lsync.Report, error) {
	rep, err := lsync.Reconcile(ctx, o)
	if err != nil || o.ReadOnly || o.StoreMirror {
		return rep, err
	}
	pending, perr := lsync.PendingProjectAdoptions(ctx, o.DB)
	if perr != nil || len(pending) == 0 {
		if perr != nil {
			rep.Warnings = append(rep.Warnings, "project adoption: "+perr.Error())
		}
		return rep, nil
	}
	if adoptFailed[root] {
		return rep, nil // failed earlier in this command: retried, and warned, once per command
	}
	n, aerr := rehomeAdoptedProjects(ctx, o, root, pending)
	if aerr != nil {
		adoptFailed[root] = true
		rep.Warnings = append(rep.Warnings, "project adoption: re-pointing core rows failed (retried on the next command): "+aerr.Error())
	}
	if n == 0 {
		return rep, nil
	}
	// The files the core just rewrote are imported into lore.db now, not
	// audited again (the core's audit sink recorded each write).
	follow, ferr := lsync.Reconcile(ctx, coreFinishOptions(o))
	if ferr != nil {
		rep.Warnings = append(rep.Warnings, "project adoption: re-import: "+ferr.Error())
		return rep, nil
	}
	rep.Exported = append(rep.Exported, follow.Exported...)
	rep.Removed = append(rep.Removed, follow.Removed...)
	rep.Deleted = append(rep.Deleted, follow.Deleted...)
	rep.Conflicts = append(rep.Conflicts, follow.Conflicts...)
	rep.Errors = append(rep.Errors, follow.Errors...)
	rep.Warnings = append(rep.Warnings, follow.Warnings...)
	rep.Diverted = append(rep.Diverted, follow.Diverted...)
	return rep, nil
}

// rehomeAdoptedProjects re-points the core-owned rows of every pending
// adoption and clears each one it finished. It returns how many rows it
// wrote (0: nothing to re-import).
func rehomeAdoptedProjects(ctx context.Context, o lsync.Options, root string, pending []lsync.ProjectAdoption) (int, error) {
	home, err := loreHome()
	if err != nil {
		home = filepath.Join(os.TempDir(), "lore-home-unresolved")
	}
	app, err := wire.New(coreOptions(root, home))
	if err != nil {
		return 0, err
	}
	defer func() { _ = app.Close() }() // the engine flushed every write it reported
	total := 0
	for _, a := range pending {
		n, err := rehomeProject(ctx, app, a.Local, a.Shared)
		total += n
		if err != nil {
			return total, err
		}
		if err := lsync.ClearProjectAdoption(ctx, o.DB, a); err != nil {
			return total, err
		}
		fmt.Fprintf(os.Stderr, "%s%s\n", syncLogPrefix, style.Success(fmt.Sprintf(
			"re-pointed %d row file(s) from local project %s to shared project %s", n, a.Local, a.Shared)))
	}
	return total, nil
}

// rehomeProject rewrites every occurrence of the local project id in the
// fields of every live core-owned row of the repo store to the shared id
// (what lsync's rewriteIDEverywhere does to lore.db), then deletes the
// local project row. The delete runs only once no live row names the
// local id any more: the engine's delete cascades to dependents, and a
// row the rewrite could not re-point must never be cascaded away.
func rehomeProject(ctx context.Context, app *wire.App, local, shared string) (int, error) {
	localID, err := ids.ParseAny(local)
	if err != nil {
		return 0, errs.Wrap(errs.InvalidID, "adopt: local project id "+local, err)
	}
	written := 0
	for _, name := range lsync.CoreOwnedTables() {
		if name == string(schema.TableProjects) {
			continue // the local project row itself is deleted below
		}
		rows, err := listCoreRows(ctx, app, row.Table(name))
		if err != nil {
			return written, err
		}
		for _, r := range rows {
			patch, err := idPatch(r, local, shared)
			if err != nil {
				return written, err
			}
			if len(patch) == 0 {
				continue
			}
			if _, err := app.Update(ctx, engine.Ref{Store: store.SourceRepo, Table: r.Table, ID: r.ID}, patch); err != nil {
				return written, fmt.Errorf("re-point %s %s: %w", r.Table, r.ID, err)
			}
			written++
		}
	}
	if left, err := rowsNaming(ctx, app, local); err != nil {
		return written, err
	} else if left != "" {
		return written, fmt.Errorf("%s still names project %s; the local project row is kept", left, local)
	}
	ref := engine.Ref{Store: store.SourceRepo, Table: schema.TableProjects, ID: localID}
	if _, err := app.Delete(ctx, ref); err != nil {
		if errs.Has(err, errs.NotFound) {
			return written, nil // a re-run: already deleted
		}
		return written, fmt.Errorf("delete local project %s: %w", local, err)
	}
	return written + 1, nil
}

// listCoreRows lists one table's live rows in the repo store; a table the
// core's registry does not define holds no core rows here.
func listCoreRows(ctx context.Context, app *wire.App, t row.Table) ([]row.Row, error) {
	eng, err := app.Engine(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := eng.Registry().Table(t); !ok {
		return nil, nil
	}
	return app.List(ctx, store.SourceRepo, t)
}

// idPatch returns the fields of r whose JSON holds oldID, with every
// occurrence replaced by newID (inferred kinds: Update re-types them to
// the table's declared kinds and validates the whole row).
func idPatch(r row.Row, oldID, newID string) (map[string]value.Value, error) {
	patch := map[string]value.Value{}
	for f, v := range r.Fields {
		raw, ok := value.JSONForm(v)
		if !ok || !bytes.Contains(raw, []byte(oldID)) {
			continue
		}
		nv, err := value.Infer(bytes.ReplaceAll(raw, []byte(oldID), []byte(newID)))
		if err != nil {
			return nil, fmt.Errorf("re-point %s %s.%s: %w", r.Table, r.ID, f, err)
		}
		patch[f] = nv
	}
	return patch, nil
}

// rowsNaming returns "<table> <id>" of a live core-owned row (the local
// project row aside) whose fields still hold id, "" when none does.
func rowsNaming(ctx context.Context, app *wire.App, id string) (string, error) {
	for _, name := range lsync.CoreOwnedTables() {
		rows, err := listCoreRows(ctx, app, row.Table(name))
		if err != nil {
			return "", err
		}
		for _, r := range rows {
			if r.ID.String() == id {
				continue
			}
			p, err := idPatch(r, id, id)
			if err != nil {
				return "", err
			}
			if len(p) > 0 {
				return name + " " + r.ID.String(), nil
			}
		}
	}
	return "", nil
}
