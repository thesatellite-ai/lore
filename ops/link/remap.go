package link

// remap.go — `lore link remap` (H-8): a rebase or an amend gives commits
// new shas, and a commit link keeps naming the old one, which then is on
// no branch. The post-rewrite hook passes git's old → new map here; a
// person can run it by hand for a rewrite made without lore's hooks.
//
//	--pair <old>=<new> (repeatable)
//	  ─► every live link of this project whose sha is <old>
//	       ├► a link <entity> ↔ <new> exists already ─► nothing new
//	       └► else create it in the same store (message, author, time
//	          from git for <new>, else the old link's)
//	     ─► the old link to the trash (reason "commit rewritten as <new>")
//
// The link id is natural on (entity_table, entity_id, sha), so a remap is
// a new link plus the old one's delete, never an edit of sha.

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// OpRemap is the operation name of `lore link remap`.
const OpRemap = "link.remap"

// pairSep separates the old and the new sha of a --pair.
const pairSep = "="

// RemapIn is the input of link.remap.
type RemapIn struct {
	Pairs []string `json:"pairs" flag:"pair" desc:"a rewritten commit: <old>=<new> (repeatable; the post-rewrite hook passes git's map)"`
}

// Remapped is one link moved to a rewritten commit.
type Remapped struct {
	// From is the old link (now in the trash) and To the link on the new
	// commit (new, or the one that already existed).
	From        string `json:"from"`
	To          string `json:"to"`
	EntityTable string `json:"entity_table"`
	EntityID    string `json:"entity_id"`
	OldSHA      string `json:"old_sha"`
	NewSHA      string `json:"new_sha"`
}

// RemapOut is the output of link.remap.
type RemapOut struct {
	Data  []Remapped `json:"data"`
	Count int        `json:"count"`
}

// Text implements op.Texter.
func (o RemapOut) Text() string {
	if len(o.Data) == 0 {
		return "(no links on the rewritten commits)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		fmt.Fprintf(&b, "✓ %s/%s: commit %s → %s  (%s → %s)\n", r.EntityTable, r.EntityID, short(r.OldSHA), short(r.NewSHA), r.From, r.To)
	}
	return b.String()
}

// remapSpec is the link.remap definition.
func remapSpec() op.Spec {
	return op.Spec{
		Name: OpRemap, Summary: "Move commit links from rewritten commits to their new shas (a rebase or an amend; the post-rewrite hook runs it)",
		Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
		Examples: []op.Example{{Title: "after a rebase made without lore's hooks", CLI: "lore link remap --pair 3c7cdd3=823df9e"}},
	}
}

// rewrite is one parsed --pair.
type rewrite struct{ old, new string }

// pairs parses and resolves --pair values: each side resolved through git
// when it can be (a short sha becomes the full one), else kept when it
// looks like a sha.
func (o ops) pairs(ctx context.Context, raw []string) ([]rewrite, error) {
	out := make([]rewrite, 0, len(raw))
	for _, p := range raw {
		oldSHA, newSHA, ok := strings.Cut(strings.TrimSpace(p), pairSep)
		oldSHA, newSHA = strings.ToLower(strings.TrimSpace(oldSHA)), strings.ToLower(strings.TrimSpace(newSHA))
		if !ok || !likelySHA(oldSHA) || !likelySHA(newSHA) {
			return nil, errs.New(errs.InvalidInput, "link remap: "+quote(p)+" is not <old>=<new>").
				WithHint("example: lore link remap --pair 3c7cdd3=823df9e")
		}
		if full, err := o.git.RevParse(ctx, "", oldSHA); err == nil {
			oldSHA = full
		}
		if full, err := o.git.RevParse(ctx, "", newSHA); err == nil {
			newSHA = full
		}
		if oldSHA != newSHA {
			out = append(out, rewrite{old: oldSHA, new: newSHA})
		}
	}
	return out, nil
}

// remap is link.remap.
func (o ops) remap(ctx context.Context, in RemapIn) (RemapOut, error) {
	rws, err := o.pairs(ctx, in.Pairs)
	if err != nil {
		return RemapOut{}, err
	}
	out := RemapOut{Data: []Remapped{}}
	if len(rws) == 0 {
		return out, nil
	}
	project, err := o.env.Project(ctx)
	if err != nil {
		return RemapOut{}, err
	}
	for _, src := range engine.ReadStoresOf(o.env) {
		rows, lerr := o.env.List(ctx, src, table)
		if lerr != nil {
			return RemapOut{}, lerr
		}
		for _, r := range rows {
			if r.Deleted || field(r, fieldProject) != project.String() {
				continue
			}
			for _, rw := range rws {
				if !sameCommit(field(r, fieldSHA), rw.old) {
					continue
				}
				done, merr := o.moveLink(ctx, src, r, rw)
				if merr != nil {
					return out, merr
				}
				out.Data = append(out.Data, done)
				break
			}
		}
	}
	out.Count = len(out.Data)
	return out, nil
}

// moveLink writes r's link on rw.new (unless it exists) in r's store and
// puts r in the trash.
func (o ops) moveLink(ctx context.Context, src store.Source, r row.Row, rw rewrite) (Remapped, error) {
	ent := field(r, schema.FieldEntityID)
	tbl := field(r, schema.FieldEntityTable)
	done := Remapped{From: r.ID.String(), EntityTable: tbl, EntityID: ent, OldSHA: field(r, fieldSHA), NewSHA: rw.new}
	have, err := engine.FindByKey(ctx, o.env, table, map[string]string{schema.FieldEntityTable: tbl, schema.FieldEntityID: ent, fieldSHA: rw.new})
	if err != nil {
		return done, err
	}
	if len(have) > 0 {
		done.To = have[0].Row.ID.String()
	} else {
		fields := make(map[string]value.Value, len(r.Fields))
		for k, v := range r.Fields {
			fields[k] = v
		}
		texts := map[string]string{fieldSHA: rw.new}
		if msg, author, when, derr := o.git.Describe(ctx, field(r, fieldRepoPath), rw.new); derr == nil { // the link's own repo, as add read it
			texts[fieldMessage], texts[fieldAuthor], texts[fieldCommittedAt] = msg, author, when
		}
		for k, v := range texts {
			if v == "" {
				continue
			}
			if fields[k], err = value.TextOf(v); err != nil {
				return done, errs.Prefix("link remap: field "+k, err)
			}
		}
		w, cerr := o.env.Create(ctx, src, table, fields)
		if cerr != nil {
			return done, cerr
		}
		done.To = w.Row.ID.String()
	}
	dctx := engine.WithDeleteReason(ctx, "commit rewritten as "+short(rw.new))
	if _, err = o.env.Delete(dctx, engine.Ref{Store: src, Table: table, ID: r.ID}); err != nil {
		return done, err
	}
	return done, nil
}
