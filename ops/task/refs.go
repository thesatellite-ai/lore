package task

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/pointer"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// `task add|edit --ref` (v0.1.17 task_refs.go, LORE_TEAM_SPEC §1, F15):
// pointers from a task to another row (<id> or <project>:<id>) are
// knowledge_refs rows (relation "mentions", confidence "extracted") in the
// task's store; a commit pointer (commit:<sha>) is a commit_links row on
// the task, the core's one home for "a commit linked to a row" (v0.1.17
// wrote it as a knowledge_refs row with dst_table "commits"; dst_id is a
// ref on the core, and a sha is not an id). Re-adding a pointer the task
// already has writes nothing.
const (
	// commitRefPrefix marks a commit pointer: --ref commit:<sha>.
	commitRefPrefix = "commit:"
	// projectRefSep separates <project>:<id> in a qualified row pointer.
	projectRefSep = ":"
	// refRelation / refConfidence are what a --ref pointer records.
	refRelation   = "mentions"
	refConfidence = schema.ConfidenceExtracted
	// Field names of knowledge_refs and commit_links.
	fieldSrcTable    = "src_table"
	fieldSrcID       = "src_id"
	fieldDstTable    = "dst_table"
	fieldDstID       = "dst_id"
	fieldDstProject  = "dst_project_id"
	fieldRelation    = "relation"
	fieldConfidence  = "confidence"
	fieldSHA         = "sha"
	tableKnowledgeRf = row.Table(schema.TableKnowledgeRefs)
	tableCommitLinks = row.Table(schema.TableCommitLinks)
)

// taskRef is one parsed --ref: a row (Table, ID, Project when qualified)
// or a commit (SHA).
type taskRef struct {
	Table   row.Table
	ID      ids.AnyID
	Project string
	SHA     string
}

// RefOut is one pointer of a task in `task show`.
type RefOut struct {
	Table     string `json:"table"`
	ID        string `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	Relation  string `json:"relation"`
	// Unavailable is set when the target row is not among the loaded
	// stores (F15 resolve): it prints "→ <project>:<id> (not available
	// here)", never an error.
	Unavailable bool `json:"unavailable,omitempty"`
}

// Where prints the pointer: "<table>/<id>", or F15's unavailable form.
func (r RefOut) Where() string {
	if !r.Unavailable {
		return r.Table + "/" + r.ID
	}
	at := r.ID
	if r.ProjectID != "" {
		at = r.ProjectID + projectRefSep + r.ID
	}
	return "→ " + at + " (not available here)"
}

// parseRefs reads --ref values. A row pointer must name an id of a known
// table; an unqualified one must exist in an attached store (a qualified
// <project>:<id> may point outside this checkout, as v0.1.17 allowed).
func (o ops) parseRefs(ctx context.Context, raw []string) ([]taskRef, error) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "task: built-in schema", err)
	}
	var out []taskRef
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if sha, ok := strings.CutPrefix(r, commitRefPrefix); ok {
			if sha == "" || strings.ContainsAny(sha, " /") {
				return nil, errs.New(errs.InvalidInput, "--ref "+r+": want commit:<sha>")
			}
			out = append(out, taskRef{SHA: sha})
			continue
		}
		project, rawID := "", r
		if p, rest, ok := strings.Cut(r, projectRefSep); ok {
			if _, perr := ids.Parse[ids.Project](p); perr != nil {
				return nil, errs.New(errs.InvalidInput, "--ref "+r+": want <id>, <prj_…>:<id> or commit:<sha>")
			}
			project, rawID = p, rest
		}
		id, perr := ids.ParseAny(rawID)
		def, known := reg.ByPrefix(id.Prefix())
		if perr != nil || !known {
			return nil, lookup.BadID("--ref "+r+": not a row id or commit:<sha>", perr)
		}
		if project == "" {
			// PRINCIPLE EXACT ERRORS / FLAG5: env.Get looks in every
			// attached store (repo, org and personal stores alike), so a
			// row of an internal store is found here and checkRefs answers
			// F15's E_LEAK_BLOCKED naming it. Only an id no attached store
			// holds is E_NOT_FOUND; a deleted row says so, and any other
			// read failure (store unreachable, …) keeps its own code
			// instead of being reported as a missing row.
			_, got, gerr := o.env.Get(ctx, engine.Ref{Table: def.Name, ID: id})
			switch {
			case errs.CodeOf(gerr) == errs.NotFound:
				return nil, errs.New(errs.NotFound, "--ref "+r+": no row "+id.String()+" in any attached store").
					WithHint("qualify a row of another project as <prj_…>:<id>")
			case gerr != nil:
				return nil, gerr
			case got.Deleted:
				return nil, errs.New(errs.NotFound, "--ref "+r+": row "+id.String()+" is deleted").
					WithHint("point at a live row")
			}
		}
		out = append(out, taskRef{Table: def.Name, ID: id, Project: project})
	}
	return out, nil
}

// checkRefs applies F15 (ops/internal/pointer) to every row pointer of
// refs as stored in a task of store from, before anything is written. A
// commit pointer names no row and is not checked.
func (o ops) checkRefs(ctx context.Context, from store.Source, refs []taskRef) error {
	for _, r := range refs {
		if r.SHA != "" {
			continue
		}
		if err := pointer.Check(ctx, o.env, from, engine.Ref{Table: r.Table, ID: r.ID}); err != nil {
			return err
		}
	}
	return nil
}

// saveRefs records refs of task id (project prj) in store src.
func (o ops) saveRefs(ctx context.Context, src store.Source, prj string, id ids.AnyID, refs []taskRef) error {
	for _, r := range refs {
		var (
			tbl row.Table
			key map[string]string
		)
		fields := map[string]value.Value{}
		if r.SHA != "" {
			tbl = tableCommitLinks
			key = map[string]string{schema.FieldEntityTable: string(table), schema.FieldEntityID: id.String(), fieldSHA: r.SHA}
			p, err := ids.ParseAny(prj)
			if err != nil {
				return errs.Wrap(errs.Internal, "task: project id", err)
			}
			if fields["project_id"], err = value.RefOf(p); err != nil {
				return err
			}
		} else {
			tbl = tableKnowledgeRf
			key = map[string]string{fieldSrcTable: string(table), fieldSrcID: id.String(), fieldDstTable: string(r.Table), fieldDstID: r.ID.String()}
			ref, err := value.RefOf(r.ID)
			if err != nil {
				return err
			}
			fields[fieldDstID] = ref
			if r.Project != "" {
				p, perr := ids.ParseAny(r.Project)
				if perr != nil {
					return errs.Wrap(errs.InvalidInput, "--ref: project", perr)
				}
				if fields[fieldDstProject], err = value.RefOf(p); err != nil {
					return err
				}
			}
			key[fieldRelation] = refRelation
			key[fieldConfidence] = refConfidence
		}
		have, err := engine.FindByKey(ctx, o.env, tbl, key)
		if err != nil {
			return err
		}
		if len(have) > 0 {
			continue
		}
		for k, v := range key {
			if k == fieldDstID || k == fieldSrcID || k == schema.FieldEntityID {
				x, perr := ids.ParseAny(v)
				if perr != nil {
					return errs.Wrap(errs.Internal, "task: ref id", perr)
				}
				if fields[k], err = value.RefOf(x); err != nil {
					return err
				}
				continue
			}
			if fields[k], err = value.TextOf(v); err != nil {
				return err
			}
		}
		if _, err := o.env.Create(ctx, src, tbl, fields); err != nil {
			return err
		}
	}
	return nil
}

// refsOf lists the pointers of task id: its knowledge_refs rows, then its
// commit links.
func (o ops) refsOf(ctx context.Context, id ids.AnyID) ([]RefOut, error) {
	var out []RefOut
	rows, err := engine.FindByKey(ctx, o.env, tableKnowledgeRf, map[string]string{fieldSrcTable: string(table), fieldSrcID: id.String()})
	if err != nil {
		return nil, err
	}
	for _, f := range rows {
		ro := RefOut{Table: text(f.Row, fieldDstTable), ID: text(f.Row, fieldDstID), ProjectID: text(f.Row, fieldDstProject), Relation: text(f.Row, fieldRelation)}
		ro.Unavailable = !o.loaded(ctx, ro.Table, ro.ID)
		out = append(out, ro)
	}
	links, err := engine.FindByKey(ctx, o.env, tableCommitLinks, map[string]string{schema.FieldEntityTable: string(table), schema.FieldEntityID: id.String()})
	if err != nil {
		return nil, err
	}
	for _, f := range links {
		out = append(out, RefOut{Table: "commits", ID: text(f.Row, fieldSHA), Relation: refRelation})
	}
	return out, nil
}

// loaded reports whether a ref target is a row of an attached store. Any
// failure to read it (unknown table, bad id, not found) counts as not
// loaded: a ref never errors on show (F15 resolve).
func (o ops) loaded(ctx context.Context, tbl, rawID string) bool {
	t, err := row.ParseTable(tbl)
	if err != nil {
		return false
	}
	id, err := ids.ParseAny(rawID)
	if err != nil {
		return false
	}
	_, r, err := o.env.Get(ctx, engine.Ref{Table: t, ID: id})
	return err == nil && !r.Deleted
}

// text is a field's string form ("" when unset).
func text(r row.Row, f string) string {
	if v, ok := r.Fields[f]; ok && v != nil {
		return v.String()
	}
	return ""
}
