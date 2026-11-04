// Package rowref is the one shared helper behind `--ref` / `--relation` on
// task, decision and incident add|edit (RES 3.11 "one shared helper", RQ7,
// OQ22). A row pointer is a knowledge_refs row (src → dst, relation,
// confidence "extracted"); a commit pointer (commit:<sha>) is a
// commit_links row on the source row.
//
//	--ref values ─▶ Parse (known table, row exists or <prj_…>:<id>)
//	             ─▶ Check (F15 direction, before anything is written)
//	(row written by the noun)
//	             ─▶ Save  (one knowledge_refs / commit_links row each; a
//	                       pointer already present writes nothing)
//	show         ─▶ List  (every pointer of a row, unavailable ones flagged)
package rowref

import (
	"context"
	"regexp"
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

// Env is what the helper needs from the engine (every noun Env has it).
type Env interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error)
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Stores() []store.Source
}

const (
	// CommitPrefix marks a commit pointer: --ref commit:<sha>.
	CommitPrefix = "commit:"
	// projectSep separates <project>:<id> in a qualified row pointer.
	projectSep = ":"
	// DefaultRelation is what --ref records without --relation (v0.1.17
	// task_refs.go wrote "mentions"; CLI_REFERENCE 2.3).
	DefaultRelation = "mentions"
	// CommitsTable is the table name a commit pointer shows as.
	CommitsTable = "commits"
	confidence   = schema.ConfidenceExtracted

	fieldSrcTable   = "src_table"
	fieldSrcID      = "src_id"
	fieldDstTable   = "dst_table"
	fieldDstID      = "dst_id"
	fieldDstProject = "dst_project_id"
	fieldRelation   = "relation"
	fieldConfidence = "confidence"
	fieldSHA        = "sha"
	fieldProject    = "project_id"
	tableRefs       = row.Table(schema.TableKnowledgeRefs)
	tableCommits    = row.Table(schema.TableCommitLinks)
)

// relationWord is MQ10/R13's snake_case identifier rule for --relation.
var relationWord = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Ref is one parsed --ref: a row (Table, ID, Project when qualified) or a
// commit (SHA).
type Ref struct {
	Table   row.Table
	ID      ids.AnyID
	Project string
	SHA     string
}

// Out is one pointer of a row as `show` prints it.
type Out struct {
	Table     string `json:"table"`
	ID        string `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	Relation  string `json:"relation"`
	// Unavailable: the target is not among the loaded stores (F15
	// resolve): "→ <project>:<id> (not available here)", never an error.
	Unavailable bool `json:"unavailable,omitempty"`
}

// Where prints the pointer: "<table>/<id>", or F15's unavailable form.
func (r Out) Where() string {
	if !r.Unavailable {
		return r.Table + "/" + r.ID
	}
	at := r.ID
	if r.ProjectID != "" {
		at = r.ProjectID + projectSep + r.ID
	}
	return "→ " + at + " (not available here)"
}

// Relation checks a --relation value ("" → DefaultRelation). reserved
// names are refused with hint (task relations owned by `task relate`,
// TASK 8.1: a --ref must not create a duplicates edge without its cascade).
func Relation(raw string, reserved map[string]string) (string, error) {
	r := strings.TrimSpace(raw)
	if r == "" {
		return DefaultRelation, nil
	}
	if !relationWord.MatchString(r) {
		return "", errs.New(errs.InvalidIdentifier, "--relation "+raw+": want a snake_case word (letters, digits, _)")
	}
	if hint, ok := reserved[r]; ok {
		return "", errs.New(errs.InvalidInput, "--relation "+r+" is reserved").WithHint(hint)
	}
	return r, nil
}

// Parse reads --ref values. A row pointer must name an id of a known
// table; an unqualified one must exist in an attached store (a qualified
// <project>:<id> may point outside this checkout, as v0.1.17 allowed).
func Parse(ctx context.Context, env Env, raw []string) ([]Ref, error) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "ref: built-in schema", err)
	}
	var out []Ref
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if sha, ok := strings.CutPrefix(r, CommitPrefix); ok {
			if sha == "" || strings.ContainsAny(sha, " /") {
				return nil, errs.New(errs.InvalidInput, "--ref "+r+": want commit:<sha>")
			}
			out = append(out, Ref{SHA: sha})
			continue
		}
		project, rawID := "", r
		if p, rest, ok := strings.Cut(r, projectSep); ok {
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
			// PRINCIPLE EXACT ERRORS / FLAG5: Get looks in every attached
			// store, so a row of an internal store is found here and
			// Check answers F15's E_LEAK_BLOCKED naming it. Only an id no
			// attached store holds is E_NOT_FOUND; any other read failure
			// keeps its own code.
			_, got, gerr := env.Get(ctx, engine.Ref{Table: def.Name, ID: id})
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
		out = append(out, Ref{Table: def.Name, ID: id, Project: project})
	}
	return out, nil
}

// Check applies F15 (ops/internal/pointer) to every row pointer as stored
// in a row of store from, before anything is written. A commit pointer
// names no row and is not checked.
func Check(ctx context.Context, env Env, from store.Source, refs []Ref) error {
	for _, r := range refs {
		if r.SHA != "" {
			continue
		}
		if err := pointer.Check(ctx, env, from, engine.Ref{Table: r.Table, ID: r.ID}); err != nil {
			return err
		}
	}
	return nil
}

// Save records refs of row id (table srcTable, project prj) in store src
// with relation rel. A pointer the row already has writes nothing.
func Save(ctx context.Context, env Env, src store.Source, srcTable row.Table, prj string, id ids.AnyID, rel string, refs []Ref) error {
	for _, r := range refs {
		var (
			tbl row.Table
			key map[string]string
		)
		fields := map[string]value.Value{}
		if r.SHA != "" {
			tbl = tableCommits
			key = map[string]string{schema.FieldEntityTable: string(srcTable), schema.FieldEntityID: id.String(), fieldSHA: r.SHA}
			p, err := ids.ParseAny(prj)
			if err != nil {
				return errs.Wrap(errs.Internal, "ref: project id", err)
			}
			if fields[fieldProject], err = value.RefOf(p); err != nil {
				return err
			}
		} else {
			tbl = tableRefs
			key = map[string]string{fieldSrcTable: string(srcTable), fieldSrcID: id.String(), fieldDstTable: string(r.Table), fieldDstID: r.ID.String(), fieldRelation: rel, fieldConfidence: confidence}
			if r.Project != "" {
				p, perr := ids.ParseAny(r.Project)
				if perr != nil {
					return errs.Wrap(errs.InvalidInput, "--ref: project", perr)
				}
				v, err := value.RefOf(p)
				if err != nil {
					return err
				}
				fields[fieldDstProject] = v
			}
		}
		have, err := engine.FindByKey(ctx, env, tbl, key)
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
					return errs.Wrap(errs.Internal, "ref: id", perr)
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
		if _, err := env.Create(ctx, src, tbl, fields); err != nil {
			return err
		}
	}
	return nil
}

// List returns the pointers of row id of srcTable: its knowledge_refs
// rows (relations in skip are left out: they belong to another view, such
// as task relate's), then its commit links.
func List(ctx context.Context, env Env, srcTable row.Table, id ids.AnyID, skip map[string]bool) ([]Out, error) {
	var out []Out
	rows, err := engine.FindByKey(ctx, env, tableRefs, map[string]string{fieldSrcTable: string(srcTable), fieldSrcID: id.String()})
	if err != nil {
		return nil, err
	}
	for _, f := range rows {
		ro := Out{Table: text(f.Row, fieldDstTable), ID: text(f.Row, fieldDstID), ProjectID: text(f.Row, fieldDstProject), Relation: text(f.Row, fieldRelation)}
		if skip[ro.Relation] {
			continue
		}
		ro.Unavailable = !loaded(ctx, env, ro.Table, ro.ID)
		out = append(out, ro)
	}
	links, err := engine.FindByKey(ctx, env, tableCommits, map[string]string{schema.FieldEntityTable: string(srcTable), schema.FieldEntityID: id.String()})
	if err != nil {
		return nil, err
	}
	for _, f := range links {
		out = append(out, Out{Table: CommitsTable, ID: text(f.Row, fieldSHA), Relation: DefaultRelation})
	}
	return out, nil
}

// loaded reports whether a ref target is a live row of an attached store;
// any read failure counts as not loaded (a ref never errors on show).
func loaded(ctx context.Context, env Env, tbl, rawID string) bool {
	t, err := row.ParseTable(tbl)
	if err != nil {
		return false
	}
	id, err := ids.ParseAny(rawID)
	if err != nil {
		return false
	}
	_, r, err := env.Get(ctx, engine.Ref{Table: t, ID: id})
	return err == nil && !r.Deleted
}

// text is a field's string form ("" when unset).
func text(r row.Row, f string) string {
	if v, ok := r.Fields[f]; ok && v != nil {
		return v.String()
	}
	return ""
}
