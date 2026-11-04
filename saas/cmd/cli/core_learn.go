// core_learn.go — `lore learn from-docs` snapshots written through the
// 0.1.18 core (snapshots are core-owned: a lore.db write would never reach
// a row file).
package main

import (
	"context"
	"time"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Fields of a snapshots row the learn import writes (schema content2Defs).
const (
	learnSnapProject    = "project_id"
	learnSnapTitle      = "title"
	learnSnapBody       = "body"
	learnSnapTakenAt    = "taken_at"
	learnSnapSourceKind = "source_kind"
	learnSnapSourceRef  = "source_ref"
)

// coreLearnSnapshot writes one learn-from snapshot of project through the
// core, unless a live snapshot of that project already has sourceRef
// (the file's path@sha: the same content is imported once). created is
// false for that skip.
func coreLearnSnapshot(ctx context.Context, projectID, title, body, sourceKind, sourceRef string, takenAt time.Time) (bool, error) {
	if coreApp == nil {
		return false, errcodes.New(errcodes.Internal, "the 0.1.18 core is not mounted")
	}
	if _, err := coreApp.Engine(ctx); err != nil { // open it: Stores is empty before
		return false, coreErr(err)
	}
	table := row.Table(schema.TableSnapshots)
	have, err := engine.FindByKey(ctx, coreApp, table, map[string]string{learnSnapProject: projectID, learnSnapSourceRef: sourceRef})
	if err != nil {
		return false, coreErr(err)
	}
	if len(have) > 0 {
		return false, nil
	}
	pid, err := ids.ParseAny(projectID)
	if err != nil {
		return false, errcodes.New(errcodes.InvalidInput, "project id "+projectID).WithCause(err)
	}
	fields := map[string]value.Value{learnSnapTakenAt: value.DateTimeFrom(takenAt)}
	if fields[learnSnapProject], err = value.RefOf(pid); err != nil {
		return false, coreErr(err)
	}
	for k, v := range map[string]string{learnSnapTitle: title, learnSnapBody: body, learnSnapSourceKind: sourceKind, learnSnapSourceRef: sourceRef} {
		if fields[k], err = value.TextOf(v); err != nil {
			return false, coreErr(err)
		}
	}
	if err := ensureCoreGitignore(ctx); err != nil {
		return false, coreErr(err)
	}
	if _, err := coreApp.Create(ctx, store.SourceRepo, table, fields); err != nil {
		return false, coreErr(err)
	}
	return true, nil
}
