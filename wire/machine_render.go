package wire

// machine_render.go — this machine's render records through the engine
// (P3-LOCAL-5, CONTEXT_BUDGET 6.11): `lore render` keeps one
// render_histories row per render in the machine store, and
// `render --trend`, `why-context` and `support-bundle` read them back.
//
//	render ─▶ App.RecordRender(rec) ─▶ App.Create(machine, render_histories)
//	trend / why-context ─▶ App.RenderHistory(project) ─▶ engine.List, oldest first
//
// The machine store holds the records of every repo on this machine, so
// each read names the project (P3-LOCAL-2); v0.1.17 read lore.db
// unscoped because that file belonged to one checkout.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// RenderRecord is one render as `lore render` keeps it.
type RenderRecord struct {
	// ID is the row's rnd_ id (set on read, ignored on write).
	ID string
	// CreatedAt is when the render was recorded (set on read).
	CreatedAt time.Time
	ProjectID string
	// RepoID is "" when the render had no repo id.
	RepoID     string
	TargetPath string
	Text       string
	SHA256     string
	TotalBytes int
	// ScopeSummary is the JSON text of the scope breakdown (with the
	// cut_summary, FA-CTX-1).
	ScopeSummary string
	// ActorID is "" when unknown.
	ActorID string
}

// RecordRender keeps rec as this machine's newest render record and
// returns its id. Required: ProjectID, TargetPath, Text, SHA256 and
// ScopeSummary (the table refuses a missing one as bad input).
func (a *App) RecordRender(ctx context.Context, rec RenderRecord) (string, error) {
	fields := map[string]value.Value{}
	texts := map[string]string{
		schema.FieldRenderProjectID: rec.ProjectID, schema.FieldRenderRepoID: rec.RepoID,
		schema.FieldRenderTarget: rec.TargetPath, schema.FieldRenderText: rec.Text,
		schema.FieldRenderSHA256: rec.SHA256, schema.FieldRenderScopeSummary: rec.ScopeSummary,
		schema.FieldRenderActorID: rec.ActorID,
	}
	if err := setTexts(fields, texts); err != nil {
		return "", err
	}
	n, err := value.NumberOf(strconv.Itoa(rec.TotalBytes))
	if err != nil {
		return "", errs.Wrap(errs.Internal, "wire: render bytes", err)
	}
	fields[schema.FieldRenderBytes] = n
	w, err := a.Create(ctx, store.SourceMachine, schema.TableRenderHistories, fields)
	if err != nil {
		return "", err
	}
	return w.Row.ID.String(), nil
}

// RenderHistory returns the project's render records, oldest first (ties
// by id, which is time ordered).
func (a *App) RenderHistory(ctx context.Context, projectID string) ([]RenderRecord, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := eng.List(ctx, store.SourceMachine, schema.TableRenderHistories)
	if err != nil {
		return nil, err
	}
	out := make([]RenderRecord, 0, len(rows))
	for _, r := range rows {
		if r.Deleted || fieldText(r, schema.FieldRenderProjectID) != projectID {
			continue
		}
		out = append(out, renderRecordOf(r))
	}
	slices.SortFunc(out, func(x, y RenderRecord) int {
		if c := x.CreatedAt.Compare(y.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
	return out, nil
}

// renderRecordOf reads a render_histories row.
func renderRecordOf(r row.Row) RenderRecord {
	rec := RenderRecord{
		ID: r.ID.String(), CreatedAt: r.CreatedAt,
		ProjectID: fieldText(r, schema.FieldRenderProjectID), RepoID: fieldText(r, schema.FieldRenderRepoID),
		TargetPath: fieldText(r, schema.FieldRenderTarget), Text: fieldText(r, schema.FieldRenderText),
		SHA256: fieldText(r, schema.FieldRenderSHA256), ScopeSummary: fieldText(r, schema.FieldRenderScopeSummary),
		ActorID: fieldText(r, schema.FieldRenderActorID),
	}
	if n, ok := r.Fields[schema.FieldRenderBytes].(value.Number); ok {
		if i, err := strconv.Atoi(n.String()); err == nil {
			rec.TotalBytes = i
		}
	}
	return rec
}

// setTexts sets each non-empty text into fields (an empty one stays unset,
// as v0.1.17's NULL / "" columns).
func setTexts(fields map[string]value.Value, texts map[string]string) error {
	for name, s := range texts {
		if s == "" {
			continue
		}
		v, err := value.TextOf(s)
		if err != nil {
			return errs.Wrap(errs.InvalidInput, "wire: field "+name, err)
		}
		fields[name] = v
	}
	return nil
}
