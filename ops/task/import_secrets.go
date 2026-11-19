package task

// Task import and the secret scan (devco D-717). An import writes one
// task per record through env.Create, and each create scanned only its
// own record: a file with keys in several records refused naming only
// the first, and several --secret-ref values were counted against that
// one record ("1 findings but 2 --secret-ref values"), so a batch could
// never be answered. Now every record is screened before the first
// write, by the planner the bulk move and store adopt use:
//
//	records ─▶ planImportSecrets (one plan row per new record)
//	   refused ─▶ every keyed record by file line / external id, the
//	              three import reruns; nothing written
//	   answered ─▶ each record's create runs with its part of the
//	              answer (SecretsForRow: its own --secret-ref values)

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/fromfile"
	"github.com/thesatellite-ai/lore/store"
)

// SecretPlanner is the optional upgrade of the env (wire.App) that
// screens a whole batch of new rows for secrets before any is written
// (engine.PlanSecretsNamed) and narrows the caller's answer to one row's
// part (engine.ForRow). An env without it leaves each create to screen
// its own row, as before.
type SecretPlanner interface {
	PlanSecretsNamed(ctx context.Context, verb, rerun string, target store.Source, rows []row.Row, names map[ids.AnyID]string) (engine.SecretPlan, error)
	SecretsForRow(ctx context.Context, p engine.SecretPlan, id ids.AnyID) context.Context
}

// importSecretFields are the record columns a task create stores as the
// person wrote them, so the ones its secret scan reads.
var importSecretFields = []string{colTitle, colBody, colKey, colRole, colCommitment}

// planImportSecrets screens every record the import is about to add
// (skip[i]: an existing task, nothing written) before the first write.
// It returns the plan and the id each record was planned under, for
// SecretsForRow; a nil slice when the env has no planner.
func (o ops) planImportSecrets(ctx context.Context, in ImportIn, from string, target store.Source, recs []importRecord, skip func(int) bool) (engine.SecretPlan, []ids.AnyID, error) {
	sp, ok := o.env.(SecretPlanner)
	if !ok {
		return engine.SecretPlan{}, nil, nil
	}
	planned := make([]ids.AnyID, len(recs))
	names := map[ids.AnyID]string{}
	rows := make([]row.Row, 0, len(recs))
	for i, r := range recs {
		if skip(i) {
			continue
		}
		id, err := ids.New[taskTag]()
		if err != nil {
			return engine.SecretPlan{}, nil, err
		}
		planned[i] = id.Any()
		fields := map[string]value.Value{}
		for _, c := range importSecretFields {
			if v := r.cols[c]; strings.TrimSpace(v) != "" {
				if fields[c], err = value.TextOf(v); err != nil {
					return engine.SecretPlan{}, nil, err
				}
			}
		}
		if len(r.checklist) > 0 {
			texts := make([]string, len(r.checklist))
			for j, it := range r.checklist {
				texts[j] = it.Text
			}
			if fields[fieldChecklist], err = value.TextOf(strings.Join(texts, "\n")); err != nil {
				return engine.SecretPlan{}, nil, err
			}
		}
		rows = append(rows, row.Row{ID: id.Any(), Table: table, Fields: fields})
		names[id.Any()] = importRecordName(r)
	}
	plan, err := sp.PlanSecretsNamed(ctx, "task import", importRerun(in, from), target, rows, names)
	return plan, planned, err
}

// importRecordName is how a refusal names a record: its CSV line and
// external id, so the person finds it in the file.
func importRecordName(r importRecord) string {
	name := "record " + r.extID
	if r.line > 0 {
		name = fromfile.Line(r.line) + " (" + r.extID + ")"
	}
	return name
}

// importRerun is the import the refusal prints to repeat with an answer:
// every flag the caller gave, with --yes (the import previews first).
func importRerun(in ImportIn, from string) string {
	line := "lore task import --file " + quoteArg(in.File) + " --from " + from
	for _, m := range in.Map {
		line += " --map " + quoteArg(m)
	}
	for _, m := range in.MapStatus {
		line += " --map-status " + quoteArg(m)
	}
	if in.To != "" {
		line += " --to " + quoteArg(in.To)
	}
	return line + " --yes"
}

// quoteArg quotes a flag value that holds a space, a quote or a comma
// list a shell would split.
func quoteArg(v string) string {
	if strings.ContainsAny(v, " \t'\"$") {
		return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
	}
	return v
}
