package task

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// SecretsTasklist is the tasklist the secret auto-tasks join, created on
// first use in the same place (SECRETS_AND_REFERENCES 7, D21).
const SecretsTasklist = "secrets"

// SecretTask is one secret auto-task: "store, then rotate" for a row
// holding ${secret:unstored} (7), or "rotate secrets <person> held" after
// lore secrets move (15). Body names rows, fields and rule ids only; never
// a value or a masked preview.
type SecretTask struct {
	Store store.Source
	Title string
	Body  string
	// Key makes it idempotent: a second call with the same key returns the
	// task already added (one task per row, not per finding).
	Key string
	// Rows are linked to the task (--ref), so `lore show <row>` shows it.
	Rows []string
}

// AddSecretTask adds t (or finds the one added with t.Key):
//
//	tasklist "secrets" in t.Store (create on first use) ─► task add --key
//	─► --ref each row (relation mentions)
//
// Lore never closes it: only rotation ends the exposure (7).
func AddSecretTask(ctx context.Context, env work.Env, t SecretTask) (id string, existed bool, err error) {
	o := ops{env: env}
	project, err := work.Project(ctx, env, Noun, "")
	if err != nil {
		return "", false, err
	}
	// idempotent in t.Store itself (task add --key looks up the default
	// place only; the auto-task lives beside the row).
	at := t.Store
	if at == "" {
		at = store.SourceRepo
	}
	rows, err := env.List(ctx, at, schema.TableTasks)
	if err != nil {
		return "", false, err
	}
	for _, r := range rows {
		if !r.Deleted && t.Key != "" && work.Field(r, work.FieldKey) == t.Key {
			return r.ID.String(), true, nil
		}
	}
	list, err := o.secretsTasklist(ctx, t.Store, project)
	if err != nil {
		return "", false, err
	}
	out, err := o.add(ctx, AddIn{Title: t.Title, Body: t.Body, Key: t.Key, Tasklist: list, Ref: t.Rows, To: string(t.Store)})
	if err != nil {
		return "", false, err
	}
	return out.Data.ID, out.existed, nil
}

// secretsTasklist finds or creates the "secrets" tasklist of project.
func (o ops) secretsTasklist(ctx context.Context, src store.Source, project ids.ID[ids.Project]) (string, error) {
	at := src
	if at == "" {
		at = store.SourceRepo
	}
	rows, err := o.env.List(ctx, at, schema.TableTasklists)
	if err != nil {
		return "", err
	}
	slices.SortFunc(rows, func(a, b row.Row) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	for _, r := range rows {
		if !r.Deleted && work.Field(r, work.FieldProject) == project.String() && work.Field(r, work.FieldTitle) == SecretsTasklist {
			return r.ID.String(), nil
		}
	}
	fields, err := work.Texts(Noun, map[string]string{work.FieldTitle: SecretsTasklist, fieldStatusStr: DefaultTasklistStatus})
	if err != nil {
		return "", err
	}
	if fields[work.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return "", err
	}
	w, err := o.env.Create(ctx, src, schema.TableTasklists, fields)
	if err != nil {
		return "", err
	}
	return w.Row.ID.String(), nil
}
