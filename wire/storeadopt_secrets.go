package wire

// storeadopt_secrets.go — the secret scan of `lore store adopt` (D-717).
// Adopt writes one rowfs batch, not an engine write per row, so it ran no
// scan: key-shaped values went into the store and --secret-unstored /
// --not-a-secret / --secret-ref did nothing. It now asks the engine the
// same questions a write does:
//
//	adoptChanges ─▶ PlanSecrets(every row to write)   refused: nothing written
//	             ─▶ per row ScreenCopy(ForRow ctx)    redacted / allow rows
//	             ─▶ the one store commit (allow rows in the same batch)
//	             ─▶ RedactTasks (store-then-rotate tasks, D21)

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

// adoptScan is the secret plan of one adopt and what applies it.
type adoptScan struct {
	eng    *engine.Engine
	target store.Source
	plan   engine.SecretPlan
	rows   []row.Row // the decoded rows of the changes, in change order
	app    *App      // the folder App eng belongs to (close)
	folder string    // the target folder's name (its project row's name)
}

// adoptScanSource names the target folder in the scan's own engine.
const adoptScanSource store.Source = "adopt-target"

// close releases the scan's folder App (nil when nothing was scanned).
func (s adoptScan) close() {
	if s.app != nil {
		_ = s.app.Close() // read-only use: a failed close leaks nothing written
	}
}

// adoptSecretPlan scans every row adopt is about to write (a dry run
// too, so the refusal comes before the question). given is the store as
// the person named it ("" for a folder named alone).
func (a *App) adoptSecretPlan(ctx context.Context, in opsstores.AdoptIn, at folderPlace, src attach.Store, given, folderName string, changes []store.Change) (adoptScan, error) {
	if len(changes) == 0 {
		return adoptScan{}, nil
	}
	// The scan runs in an engine over the target folder itself (and the
	// machine store): its policy, rules and allow entries are the ones a
	// write into that folder sees, from a code repo or from any folder.
	_, org := a.removePlace(src, at.Name, "")
	x, err := a.openFolderApp(ctx, org, attach.Store{Name: adoptScanSource, URL: src.URL, Branch: src.Branch, Folder: folderName})
	if err != nil {
		return adoptScan{}, errs.Prefix(adoptVerb+": the secret scan of the rows to adopt could not start; nothing written", err)
	}
	eng, err := x.Engine(ctx)
	if err != nil {
		_ = x.Close()
		return adoptScan{}, errs.Prefix(adoptVerb+": the secret scan of the rows to adopt could not start; nothing written", err)
	}
	target := adoptScanSource
	rows := make([]row.Row, len(changes))
	for i, c := range changes {
		if rows[i], err = c.Row.Decode(); err != nil {
			_ = x.Close()
			return adoptScan{}, err
		}
	}
	plan, err := eng.PlanSecrets(ctx, adoptVerb, adoptRerun(in, given, folderName), target, rows)
	if err != nil {
		_ = x.Close()
		return adoptScan{}, err
	}
	return adoptScan{eng: eng, app: x, target: target, plan: plan, rows: rows, folder: folderName}, nil
}

// adoptRerun is the adopt command to repeat with a secret answer.
func adoptRerun(in opsstores.AdoptIn, given, folderName string) string {
	t := folderName
	if given != "" {
		t = given + "/" + folderName
	}
	cmd := "lore store adopt --target " + adoptArg(t)
	if in.From != "" {
		cmd += " --from " + adoptArg(in.From)
	}
	for _, f := range []struct {
		on   bool
		flag string
	}{{in.Move, "--move"}, {in.Own, "--own"}, {in.RemoveSource && !in.Move, "--remove-source"}, {in.Commit, "--commit"}} {
		if f.on {
			cmd += " " + f.flag
		}
	}
	return cmd + " --yes"
}

// apply answers each planned row as a write does: redacted fields
// replace the row (its HLC kept), allow rows join the batch, and the
// redactions are returned for the store-then-rotate tasks.
func (s adoptScan) apply(ctx context.Context, changes []store.Change) ([]store.Change, []engine.RedactedWrite, error) {
	if len(s.plan.Rows) == 0 {
		return changes, nil, nil
	}
	planned := map[string]bool{}
	for _, id := range s.plan.Rows {
		planned[id.String()] = true
	}
	out := make([]store.Change, 0, len(changes))
	var red []engine.RedactedWrite
	for i, c := range changes {
		r := s.rows[i]
		if !planned[r.ID.String()] {
			out = append(out, c)
			continue
		}
		got, err := s.eng.ScreenCopy(s.eng.ForRow(ctx, s.plan, r.ID), s.target, r)
		if err != nil {
			return nil, nil, errs.Prefix(adoptVerb+"; nothing written", err)
		}
		sr, err := store.FromRow(got.Row)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, store.Change{Row: sr, Expect: c.Expect})
		for _, al := range got.Allows {
			ar, aerr := store.FromRow(al)
			if aerr != nil {
				return nil, nil, aerr
			}
			out = append(out, store.Change{Row: ar})
		}
		if !got.Redacted.ID.IsZero() {
			red = append(red, got.Redacted)
		}
	}
	return out, red, nil
}

// adoptArg is s as one shell word: quoted only when it needs to be.
func adoptArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\*?&;|<>()#") {
		return s
	}
	return shellQuote(s)
}

// redactTasks adds the store-then-rotate task of each redacted row (D21).
//
// The task and its "secrets" list name the folder's project, so its row
// is made first when the folder has none (devco:D-762): adopt leaves the
// repo's project row behind and a fresh folder has no row of its own, so
// the list's project_id named a row no store held and the task was never
// added, though --secret-unstored says it is.
func (s adoptScan) redactTasks(ctx context.Context, ws []engine.RedactedWrite) error {
	if s.eng == nil || len(ws) == 0 {
		return nil
	}
	// adopt is registered as a read (its rows go in one git commit, not
	// through the engine); the project row and the task are engine writes
	ctx = engine.WithAccess(ctx, op.AccessWrite)
	st, err := s.app.state(ctx)
	if err != nil {
		return errs.Prefix("the rows are stored but a store-then-rotate task failed (lore secrets check adds it)", err)
	}
	if _, err := s.app.rosterProject(ctx, st, s.target, s.folder); err != nil {
		return errs.Prefix("the rows are stored but a store-then-rotate task failed (lore secrets check adds it)", err)
	}
	return s.eng.RedactTasks(ctx, ws)
}
