package task

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Relations (TASK 8). `blocked_by` (the --after edges on the waiting task)
// stays the ONLY blocking mechanism (8.1: two stores for one fact is the
// failure mode); every other relation is a knowledge_refs row.
//
//	relate ─► --blocked-by / --blocks ─► add an edge to blocked_by of the
//	   │                                 waiting row (same bytes as edit --after)
//	   ├─► --relates-to / --follows-up ─► knowledge_refs row (dedupe by key)
//	   └─► --duplicates ─► E_CYCLE check over the duplicate chain
//	        ─► knowledge_refs row ─► copy comments + new checklist texts
//	        ─► cancel the duplicate, close_reason duplicate
//	unrelate ─► the inverse (removes the edge / the refs rows)
//	show --relations ─► both directions, duplicates resolved to the root

// Operation names.
const (
	OpRelate   = "task.relate"
	OpUnrelate = "task.unrelate"
)

// Relation names stored in knowledge_refs.relation. Closed set registered
// here (8.1: a typo cannot create a new kind; TestRelationNames guards it).
const (
	RelDuplicates = "duplicates"
	RelRelatesTo  = "relates_to"
	RelFollowsUp  = "follows_up"
)

// Relations returns the closed set of task relation names (blocking is
// the blocked_by field, not a name here).
func Relations() []string { return []string{RelDuplicates, RelRelatesTo, RelFollowsUp} }

// closeReasonDuplicate is the close_reason a duplicate gets (8.3).
const closeReasonDuplicate = "duplicate"

// RelateIn is the input of task.relate and task.unrelate.
type RelateIn struct {
	ID         string   `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Duplicates string   `json:"duplicates,omitempty" flag:"duplicates" desc:"this task duplicates that one (tsk_…): it is cancelled, close_reason duplicate"`
	RelatesTo  []string `json:"relates_to,omitempty" flag:"relates-to" desc:"see also: any row id; repeatable"`
	FollowsUp  []string `json:"follows_up,omitempty" flag:"follows-up" desc:"this task was created because of that row; repeatable"`
	BlockedBy  []string `json:"blocked_by,omitempty" flag:"blocked-by" desc:"this task waits for that task (same as edit --after); repeatable"`
	Blocks     []string `json:"blocks,omitempty" flag:"blocks" desc:"that task waits for this one (writes blocked_by on it); repeatable"`
}

// RelateOut is the output of relate / unrelate.
type RelateOut struct {
	ID      string   `json:"id"`
	Changed []string `json:"changed"`
	Human   string   `json:"-"`
}

// Text implements the human output.
func (o RelateOut) Text() string { return o.Human }

// ShowRelations is show --relations (both directions).
type ShowRelations struct {
	Blocks       []RelatedTask `json:"blocks,omitempty"`
	BlockedBy    []RelatedTask `json:"blocked_by,omitempty"`
	Duplicates   RelatedTask   `json:"duplicates,omitzero"`
	DuplicatedBy []RelatedTask `json:"duplicated_by,omitempty"`
	RelatesTo    []RelatedTask `json:"relates_to,omitempty"`
	FollowsUp    []RelatedTask `json:"follows_up,omitempty"`
	FollowedBy   []RelatedTask `json:"followed_up_by,omitempty"`
	shown        bool
}

// RelatedTask is one related row.
type RelatedTask struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	// Root is the original a duplicate chain resolves to (8.4), when it
	// differs from ID.
	Root string `json:"root,omitempty"`
}

func relateOps(o ops) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRelate, Summary: "Relate a task to another (duplicates, relates-to, follows-up, blocked-by, blocks)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), errs.Cycle, errs.OpenChildren),
			Examples: []op.Example{{Title: "mark a duplicate", CLI: "lore task relate tsk_<a> --duplicates tsk_<b>", Want: "duplicates"}},
		}, o.relate),
		op.Define(op.Spec{
			Name: OpUnrelate, Summary: "Remove a relation added with task relate",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
		}, o.unrelate),
	}
}

// relate adds every relation the flags name, in flag order (TASK 8.2).
func (o ops) relate(ctx context.Context, in RelateIn) (RelateOut, error) {
	id, err := taskID(in.ID)
	if err != nil {
		return RelateOut{}, err
	}
	src, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return RelateOut{}, err
	}
	out := RelateOut{ID: id.String(), Changed: []string{}}
	var b strings.Builder
	for _, x := range in.BlockedBy {
		if err = o.addEdge(ctx, id.String(), x); err != nil {
			return RelateOut{}, err
		}
		fmt.Fprintf(&b, "%s %s waits for %s\n", work.Success, id, x)
		out.Changed = append(out.Changed, id.String())
	}
	for _, x := range in.Blocks {
		if err = o.addEdge(ctx, x, id.String()); err != nil {
			return RelateOut{}, err
		}
		fmt.Fprintf(&b, "%s %s waits for %s\n", work.Success, x, id)
		out.Changed = append(out.Changed, x)
	}
	for _, pair := range []struct {
		rel  string
		dsts []string
	}{{RelRelatesTo, in.RelatesTo}, {RelFollowsUp, in.FollowsUp}} {
		for _, d := range pair.dsts {
			if err = o.addRelation(ctx, src, cur, pair.rel, d); err != nil {
				return RelateOut{}, err
			}
			fmt.Fprintf(&b, "%s %s %s %s\n", work.Success, id, strings.ReplaceAll(pair.rel, "_", " "), d)
		}
	}
	if in.Duplicates != "" {
		line, err := o.duplicate(ctx, src, cur, in.Duplicates)
		if err != nil {
			return RelateOut{}, err
		}
		b.WriteString(line)
		out.Changed = append(out.Changed, id.String())
	}
	if b.Len() == 0 {
		return RelateOut{}, errs.New(errs.InvalidInput, "task relate: name a relation").WithHint("--duplicates | --relates-to | --follows-up | --blocked-by | --blocks")
	}
	out.Human = b.String()
	return out, nil
}

// unrelate removes the relations the flags name. A relation that is not
// there is a no-op line, never an error (run twice = same state).
func (o ops) unrelate(ctx context.Context, in RelateIn) (RelateOut, error) {
	id, err := taskID(in.ID)
	if err != nil {
		return RelateOut{}, err
	}
	if _, _, err = work.Get(ctx, o.env, Noun, table, id); err != nil {
		return RelateOut{}, err
	}
	out := RelateOut{ID: id.String(), Changed: []string{}}
	var b strings.Builder
	edge := func(waiter, up string) error {
		changed, rerr := o.removeEdge(ctx, waiter, up)
		if rerr != nil {
			return rerr
		}
		if changed {
			out.Changed = append(out.Changed, waiter)
			fmt.Fprintf(&b, "%s %s no longer waits for %s\n", work.Success, waiter, up)
		} else {
			fmt.Fprintf(&b, "  %s did not wait for %s\n", waiter, up)
		}
		return nil
	}
	for _, x := range in.BlockedBy {
		if err = edge(id.String(), x); err != nil {
			return RelateOut{}, err
		}
	}
	for _, x := range in.Blocks {
		if err = edge(x, id.String()); err != nil {
			return RelateOut{}, err
		}
	}
	rels := map[string][]string{RelRelatesTo: in.RelatesTo, RelFollowsUp: in.FollowsUp}
	if in.Duplicates != "" {
		rels[RelDuplicates] = []string{in.Duplicates}
	}
	for _, rel := range Relations() {
		for _, d := range rels[rel] {
			n, err := o.removeRelation(ctx, id.String(), rel, d)
			if err != nil {
				return RelateOut{}, err
			}
			fmt.Fprintf(&b, "%s %d %s relation(s) to %s removed\n", work.Success, n, rel, d)
		}
	}
	if b.Len() == 0 {
		return RelateOut{}, errs.New(errs.InvalidInput, "task unrelate: name a relation").WithHint("--duplicates | --relates-to | --follows-up | --blocked-by | --blocks")
	}
	out.Human = b.String()
	return out, nil
}

// addEdge adds `waiter --after up` to waiter's blocked_by, through the
// same parse / cycle check / encoding as edit --after (8.5: byte-for-byte).
// An edge already there writes nothing.
func (o ops) addEdge(ctx context.Context, waiter, up string) error {
	wid, err := taskID(waiter)
	if err != nil {
		return err
	}
	_, w, err := work.Get(ctx, o.env, Noun, table, wid)
	if err != nil {
		return err
	}
	deps := depsOf(w)
	for _, d := range deps {
		if d.TaskID == up {
			return nil
		}
	}
	add, err := o.parseAfter(ctx, []string{up}, work.Field(w, work.FieldRepo))
	if err != nil {
		return err
	}
	deps = append(deps, add...)
	if err = o.checkNoCycle(ctx, waiter, deps); err != nil {
		return err
	}
	v, err := depsValue(deps)
	if err != nil {
		return err
	}
	_, err = work.Patch(ctx, o.hist(), Noun, table, wid, map[string]value.Value{fieldBlockedBy: v}, "")
	return err
}

// removeEdge drops `waiter --after up`; false when it was not there.
func (o ops) removeEdge(ctx context.Context, waiter, up string) (bool, error) {
	wid, err := taskID(waiter)
	if err != nil {
		return false, err
	}
	_, w, err := work.Get(ctx, o.env, Noun, table, wid)
	if err != nil {
		return false, err
	}
	deps := depsOf(w)
	kept := slices.DeleteFunc(slices.Clone(deps), func(d dep) bool { return d.TaskID == up })
	if len(kept) == len(deps) {
		return false, nil
	}
	var v value.Value = value.Null{}
	if len(kept) > 0 {
		if v, err = depsValue(kept); err != nil {
			return false, err
		}
	}
	_, err = work.Patch(ctx, o.hist(), Noun, table, wid, map[string]value.Value{fieldBlockedBy: v}, "")
	return true, err
}

// relKey is the dedupe key of a relation row (8.4: readers dedupe by
// (src, dst, relation)).
func relKey(srcID, rel string, dst ids.AnyID) map[string]string {
	return map[string]string{fieldSrcTable: string(table), fieldSrcID: srcID, fieldDstTable: tableOf(dst), fieldDstID: dst.String(), fieldRelation: rel}
}

// tableOf names the table an id's prefix belongs to ("" when unknown).
func tableOf(id ids.AnyID) string {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return ""
	}
	t, ok := reg.ByPrefix(id.Prefix())
	if !ok {
		return ""
	}
	return string(t.Name)
}

// addRelation writes one knowledge_refs row from cur to dst (any row id
// an attached store holds), unless the same (src, dst, relation) exists.
func (o ops) addRelation(ctx context.Context, src store.Source, cur row.Row, rel, dst string) error {
	did, err := ids.ParseAny(dst)
	if err != nil {
		return errs.Wrap(errs.InvalidID, "task relate: "+dst, err)
	}
	if did.String() == cur.ID.String() {
		return errs.New(errs.InvalidInput, "task relate: a task cannot relate to itself")
	}
	if tableOf(did) == "" || !o.loaded(ctx, tableOf(did), did.String()) {
		return errs.New(errs.NotFound, "task relate: no row "+dst+" in the attached stores")
	}
	key := relKey(cur.ID.String(), rel, did)
	have, err := engine.FindByKey(ctx, o.env, tableKnowledgeRf, key)
	if err != nil || len(have) > 0 {
		return err
	}
	fields := map[string]value.Value{}
	for k, v := range key {
		if k == fieldSrcID || k == fieldDstID {
			x, perr := ids.ParseAny(v)
			if perr != nil {
				return errs.Wrap(errs.Internal, "task: relation id", perr)
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
	if fields[fieldConfidence], err = value.TextOf(refConfidence); err != nil {
		return err
	}
	_, err = o.env.Create(ctx, src, tableKnowledgeRf, fields)
	return err
}

// removeRelation tombstones every (src, dst, relation) row (duplicates
// from two machines included, 8.4) and returns how many.
func (o ops) removeRelation(ctx context.Context, srcID, rel, dst string) (int, error) {
	did, err := ids.ParseAny(dst)
	if err != nil {
		return 0, errs.Wrap(errs.InvalidID, "task unrelate: "+dst, err)
	}
	have, err := engine.FindByKey(ctx, o.env, tableKnowledgeRf, relKey(srcID, rel, did))
	if err != nil {
		return 0, err
	}
	for _, h := range have {
		if _, err = o.env.Delete(ctx, engine.Ref{Store: h.Store, Table: tableKnowledgeRf, ID: h.Row.ID}); err != nil {
			return 0, err
		}
	}
	return len(have), nil
}

// duplicate is 8.2's --duplicates cascade: refuse a duplicate cycle
// (E_CYCLE), record the relation, copy comments and checklist texts the
// original lacks (originals kept), cancel the duplicate with close_reason
// duplicate (subtree rules of cancel apply: E_OPEN_CHILDREN).
func (o ops) duplicate(ctx context.Context, src store.Source, cur row.Row, orig string) (string, error) {
	oid, err := taskID(orig)
	if err != nil {
		return "", err
	}
	self := cur.ID.String()
	if oid.String() == self {
		return "", errs.New(errs.Cycle, "task relate: a task cannot duplicate itself")
	}
	if chain := o.dupChain(ctx, oid.String()); slices.Contains(chain, self) {
		return "", errs.New(errs.Cycle, "task relate: duplicate cycle: "+strings.Join(append([]string{self}, chain...), " → "))
	}
	if err = o.addRelation(ctx, src, cur, RelDuplicates, oid.String()); err != nil {
		return "", err
	}
	comments, items, err := o.copyToOriginal(ctx, cur, oid)
	if err != nil {
		return "", err
	}
	if _, err = o.cancel(ctx, CancelIn{ID: self, Reason: closeReasonDuplicate}); err != nil {
		return "", err
	}
	line := fmt.Sprintf("%s %s duplicates %s; %s cancelled (close_reason duplicate)\n", work.Success, self, oid, self)
	if comments+items > 0 {
		line += fmt.Sprintf("  copied to %s: %d comment(s), %d checklist item(s) not already there (originals kept)\n", oid, comments, items)
	}
	return line, nil
}

// dupChain follows `duplicates` from id to its root (ids after id, in
// order); a chain that loops stops at the repeat.
func (o ops) dupChain(ctx context.Context, id string) []string {
	var chain []string
	seen := map[string]bool{id: true}
	for at := id; ; {
		rows, err := engine.FindByKey(ctx, o.env, tableKnowledgeRf, map[string]string{fieldSrcTable: string(table), fieldSrcID: at, fieldRelation: RelDuplicates})
		if err != nil || len(rows) == 0 {
			return chain
		}
		next := text(rows[0].Row, fieldDstID)
		if seen[next] {
			return append(chain, next)
		}
		seen[next] = true
		chain = append(chain, next)
		at = next
	}
}

// copyToOriginal copies the duplicate's comments and the checklist texts
// the original does not have yet onto the original.
func (o ops) copyToOriginal(ctx context.Context, dup row.Row, orig ids.AnyID) (comments, items int, err error) {
	h, err := o.getChecklist(ctx, orig.String())
	if err != nil {
		return 0, 0, err
	}
	have := map[string]bool{}
	for _, it := range Checklist(h.task) {
		have[it.Text] = true
	}
	var texts []string
	var done []bool
	for _, it := range Checklist(dup) {
		if !it.Deleted() && !have[it.Text] {
			texts, done = append(texts, it.Text), append(done, it.Done)
			have[it.Text] = true
		}
	}
	if len(texts) > 0 {
		if _, err = o.appendItems(ctx, &h, texts, done); err != nil {
			return 0, 0, err
		}
		if _, err = o.saveChecklist(ctx, h, nil, ""); err != nil {
			return 0, 0, err
		}
	}
	notes, err := engine.FindByKey(ctx, o.env, row.Table(schema.TableComments), map[string]string{schema.FieldEntityTable: string(table), schema.FieldEntityID: dup.ID.String()})
	if err != nil {
		return 0, 0, err
	}
	for _, n := range notes {
		fields := map[string]value.Value{}
		for k, v := range n.Row.Fields {
			fields[k] = v
		}
		if fields[schema.FieldEntityID], err = value.RefOf(orig); err != nil {
			return 0, 0, err
		}
		if _, err = o.env.Create(ctx, h.src, row.Table(schema.TableComments), fields); err != nil {
			return 0, 0, err
		}
	}
	return len(notes), len(texts), nil
}

// relationsOf builds show --relations for task id.
func (o ops) relationsOf(ctx context.Context, cur row.Row) (ShowRelations, error) {
	id := cur.ID.String()
	rel := ShowRelations{shown: true}
	f, err := o.forest(ctx, "")
	if err != nil {
		return ShowRelations{}, err
	}
	brief := func(x string) RelatedTask {
		s, ok := f.rows[x]
		if !ok {
			return RelatedTask{ID: x}
		}
		return RelatedTask{ID: x, Title: work.Field(s.Row, work.FieldTitle), Status: work.Field(s.Row, work.FieldStatus)}
	}
	for _, d := range depsOf(cur) {
		rel.BlockedBy = append(rel.BlockedBy, brief(d.TaskID))
	}
	ids := make([]string, 0, len(f.rows))
	for k := range f.rows {
		ids = append(ids, k)
	}
	slices.Sort(ids)
	for _, k := range ids {
		for _, d := range depsOf(f.rows[k].Row) {
			if d.TaskID == id {
				rel.Blocks = append(rel.Blocks, brief(k))
			}
		}
	}
	out, err := engine.FindByKey(ctx, o.env, tableKnowledgeRf, map[string]string{fieldSrcTable: string(table), fieldSrcID: id})
	if err != nil {
		return ShowRelations{}, err
	}
	seen := map[string]bool{}
	for _, r := range out {
		dst, rn := text(r.Row, fieldDstID), text(r.Row, fieldRelation)
		if seen[rn+dst] {
			continue
		}
		seen[rn+dst] = true
		switch rn {
		case RelDuplicates:
			t := brief(dst)
			if chain := o.dupChain(ctx, dst); len(chain) > 0 {
				t.Root = chain[len(chain)-1]
			}
			rel.Duplicates = t
		case RelRelatesTo:
			rel.RelatesTo = append(rel.RelatesTo, brief(dst))
		case RelFollowsUp:
			rel.FollowsUp = append(rel.FollowsUp, brief(dst))
		}
	}
	in, err := engine.FindByKey(ctx, o.env, tableKnowledgeRf, map[string]string{fieldDstID: id})
	if err != nil {
		return ShowRelations{}, err
	}
	for _, r := range in {
		s, rn := text(r.Row, fieldSrcID), text(r.Row, fieldRelation)
		if seen["in"+rn+s] {
			continue
		}
		seen["in"+rn+s] = true
		switch rn {
		case RelDuplicates:
			rel.DuplicatedBy = append(rel.DuplicatedBy, brief(s))
		case RelRelatesTo: // shown both ways (8.1)
			rel.RelatesTo = append(rel.RelatesTo, brief(s))
		case RelFollowsUp:
			rel.FollowedBy = append(rel.FollowedBy, brief(s))
		}
	}
	return rel, nil
}

// text renders show --relations.
func (r *ShowRelations) text() string {
	var b strings.Builder
	line := func(label string, ts []RelatedTask) {
		for _, t := range ts {
			s := fmt.Sprintf("  %-14s %s %s", label+":", t.ID, t.Title)
			if t.Status != "" {
				s += " (" + t.Status + ")"
			}
			b.WriteString(strings.TrimRight(s, " ") + "\n")
		}
	}
	line("blocks", r.Blocks)
	line("waits for", r.BlockedBy)
	if r.Duplicates.ID != "" {
		t := r.Duplicates
		if t.Root != "" && t.Root != t.ID {
			t.Title += " → root " + t.Root
		}
		line("duplicates", []RelatedTask{t})
	}
	line("duplicated by", r.DuplicatedBy)
	line("relates to", r.RelatesTo)
	line("follows up", r.FollowsUp)
	line("followed up by", r.FollowedBy)
	if b.Len() == 0 {
		return "  relations: none\n"
	}
	return b.String()
}
