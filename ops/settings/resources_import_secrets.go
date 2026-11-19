package settings

// Resource import and the secret scan (devco D-717). An import stages one
// resAdd / resEdit per record, and each write scanned only its own record:
// a file with keys in several records refused naming only the first, and
// several --secret-ref values were counted against that one record
// ("1 findings but 2 --secret-ref values"), so a batch could never be
// answered. Now every record is screened before the first write, by the
// planner task import, the bulk move and store adopt use:
//
//	records ─▶ planResImportSecrets (the exact fields each write stores,
//	           under its pinned or existing id)
//	   refused ─▶ every keyed record by kind/name, the three import
//	              reruns; nothing written
//	   answered ─▶ each record's write runs with its part of the answer
//	              (SecretsForRow: its own --secret-ref values)

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// SecretPlanner is the optional upgrade of the env (wire.App) that
// screens a whole batch of rows for secrets before any is written
// (engine.PlanSecretsNamed) and narrows the caller's answer to one row's
// part (engine.ForRow). An env without it leaves each write to screen its
// own row, as before.
type SecretPlanner interface {
	PlanSecretsNamed(ctx context.Context, verb, rerun string, target store.Source, rows []row.Row, names map[ids.AnyID]string) (engine.SecretPlan, error)
	SecretsForRow(ctx context.Context, p engine.SecretPlan, id ids.AnyID) context.Context
}

// resImportVerb starts a resource import's batch secret refusal.
const resImportVerb = "resource import"

// planResImportSecrets screens every record of plan before the first
// write: a new record as the fields resAdd stores under the id pinned for
// it (ctx carries the pending rows, so refs to other records resolve), an
// upsert as the patch resEdit writes to its row (existing: ext id → id).
// It returns the plan and the id each record was planned under, for
// SecretsForRow; a zero plan when the env has no planner.
func (o ops) planResImportSecrets(ctx context.Context, in ResImportIn, format string, target store.Source, plan []plannedRecord, existing map[string]string) (engine.SecretPlan, map[string]ids.AnyID, error) {
	sp, ok := o.env.(SecretPlanner)
	if !ok {
		return engine.SecretPlan{}, nil, nil
	}
	planned := map[string]ids.AnyID{} // ext id → row id
	names := map[ids.AnyID]string{}
	rows := make([]row.Row, 0, len(plan))
	for _, p := range plan {
		sets := append(slices.Clone(p.first), p.later...)
		d, err := o.kindDef(ctx, p.kind)
		if err != nil {
			return engine.SecretPlan{}, nil, err
		}
		var r row.Row
		if id, up := existing[p.extID]; up {
			if r, err = o.resEditPlanRow(ctx, d, id, sets); err != nil {
				return engine.SecretPlan{}, nil, err
			}
		} else {
			fields, _, _, ferr := o.resAddFields(ctx, d, p.name, sets)
			if ferr != nil {
				return engine.SecretPlan{}, nil, ferr
			}
			pid, pinned := pinnedID(ctx, d.kind.Row.ID.String(), p.name)
			if !pinned {
				continue // pendingOf pins every new record; nothing to key the answer on
			}
			r = row.Row{ID: pid, Table: schema.TableResources, Fields: fields}
		}
		planned[p.extID] = r.ID
		names[r.ID] = p.kind + "/" + p.name
		rows = append(rows, r)
	}
	sec, err := sp.PlanSecretsNamed(ctx, resImportVerb, resImportRerun(in, format), target, rows, names)
	return sec, planned, err
}

// resEditPlanRow is the row an upsert's resEdit writes for sets: the
// stored resource with its values patched (no write).
func (o ops) resEditPlanRow(ctx context.Context, d kindDef, id string, sets []string) (row.Row, error) {
	r, err := o.resourceInScope(ctx, id, OpResEdit)
	if err != nil {
		return row.Row{}, err
	}
	stored, err := d.decode(r)
	if err != nil {
		return row.Row{}, err
	}
	if err = o.applySets(ctx, d, text(r.Row, rName), sets, stored.vals); err != nil {
		return row.Row{}, err
	}
	patch, err := o.valuesPatch(ctx, d, stored.vals, stored)
	if err != nil {
		return row.Row{}, err
	}
	return row.Row{ID: r.Row.ID, Table: schema.TableResources, Fields: patch}, nil
}

// resImportRerun is the import the refusal prints to repeat with an
// answer: every flag the caller gave, with --yes (an import always asks).
func resImportRerun(in ResImportIn, format string) string {
	line := "lore resource import --file " + shellArg(in.File) + " --from " + format
	if in.Kind != "" {
		line += " --kind " + shellArg(in.Kind)
	}
	if in.UpsertBy != "" {
		line += " --upsert-by " + in.UpsertBy
	}
	if in.To != "" {
		line += " --to " + shellArg(in.To)
	}
	if in.Overwrite {
		line += " --overwrite"
	}
	if in.SkipErrors {
		line += " --skip-errors"
	}
	return line + " --yes"
}

// shellArg quotes a flag value a shell would split or expand, so the
// printed rerun runs as printed.
func shellArg(v string) string {
	if strings.ContainsAny(v, " \t'\"$`\\*?;&|<>()") {
		return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
	}
	return v
}
