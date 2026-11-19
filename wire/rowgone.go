package wire

// rowgone.go — what a row file found by id says when it is no live row
// (devco:D-589, D-590, owner use pass 2 Wrong 5 and 6). The id lookups
// outside a repo (targetoutside.go rowFolderIn) and in a repo or store
// clone (idfolder.go) glob <folder>/<table>/<id>.json across the org's
// store clones. A tombstone matched that glob as if the row lived there:
// the read then said "it is in <org>/<folder>" (or, from the home folder,
// gave the branch hint) and answered not found. An id found nowhere fell
// through to the folder's own refusal, which loops between --setup and
// "--store does not apply". Both now get one answer naming the id:
//
//	tombstone in <org>/<folder> ─▶ "deleted from <org>/<folder> on <date>"
//	                               + `lore trash list --store <org>/<folder>`
//	  …and listed in its _purged.json ─▶ E_PURGED "was purged from …": a
//	                               purge is gone for good, so never the
//	                               trash list or restore
//	no file anywhere            ─▶ "in no store of any setup on this machine"
//	                               + the folders lore looked in

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// goneRow is a tombstone of the row an id names: the folder it was
// deleted from (as `--store` names it) and the day it was. purged is set
// when that folder lists the id as purged (a `lore trash purge` writes
// the tombstone and the list entry together): err is then E_PURGED, as
// the engine and the live ref answer a purged id, never the trash list.
type goneRow struct {
	id, place, day string
	purged         bool
}

// tombstoneDay reports whether the row file at path is a tombstone and,
// when it is, the day of its last stamp (YYYY-MM-DD, "" when unknown).
// Only the two envelope keys are read: a tombstone carries no body.
func tombstoneDay(path string) (string, bool) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", false
	}
	var env struct {
		Deleted bool   `json:"_deleted"`
		HLC     string `json:"_hlc"`
	}
	if json.Unmarshal(row.TrimBOM(b), &env) != nil || !env.Deleted {
		return "", false
	}
	wall, _, _ := strings.Cut(env.HLC, "/")
	t, err := time.Parse(time.RFC3339Nano, wall)
	if err != nil {
		return "", true
	}
	return t.UTC().Format(dayLayout), true
}

// dayLayout is the date a deleted-row answer prints.
const dayLayout = "2006-01-02"

// liveHit is the first of hits that is a live row file; when every hit
// is a tombstone, gone describes the first of them in place.
func liveHit(hits []string, id, place string) (live bool, gone *goneRow) {
	for _, h := range hits {
		day, dead := tombstoneDay(h)
		if !dead {
			return true, nil
		}
		if gone == nil {
			gone = &goneRow{id: id, place: place, day: day, purged: purgedInFolderOf(h, id)}
		}
	}
	return false, gone
}

// liveFile is the first of hits that is no tombstone ("" when none).
func liveFile(hits []string) string {
	for _, h := range hits {
		if _, dead := tombstoneDay(h); !dead {
			return h
		}
	}
	return ""
}

// purgedInFolderOf reports whether the folder holding the row file at
// path (<folder>/<table>/<id>.json) lists id in its _purged.json. A list
// that cannot be read counts as purged: claiming a purged row is still
// restorable would send the user to a trash that cannot bring it back.
func purgedInFolderOf(path, id string) bool {
	list := filepath.Join(filepath.Dir(filepath.Dir(path)), rowfs.PurgedFileName)
	b, err := os.ReadFile(filepath.Clean(list))
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	var doc struct {
		IDs []json.RawMessage `json:"ids"`
	}
	if json.Unmarshal(row.TrimBOM(b), &doc) != nil {
		return true
	}
	for _, raw := range doc.IDs {
		var s string
		if json.Unmarshal(raw, &s) == nil && s == id {
			return true
		}
	}
	return false
}

// purgedHintOutside is the hint of a purged row's answer, as the
// engine's E_PURGED gives it (engine/purge.go purgedErr).
func purgedHintOutside(id string) string {
	return "read what it held: lore trash show --id " + id + " --from-history; then write what you still need as a new row (a new id)"
}

// err is the deleted row's answer: E_PURGED for a purged row (gone for
// good: no trash to list), else E_NOT_FOUND naming where and when it was
// deleted and the command that lists that folder's trash.
func (g *goneRow) err() error {
	if g.purged {
		msg := g.id + " was purged from " + g.place
		if g.day != "" {
			msg += " on " + g.day
		}
		msg += "; purged rows never come back"
		return &purgedGoneErr{
			purged:   errs.New(errs.Purged, msg).WithHint(purgedHintOutside(g.id)),
			notFound: errs.New(errs.NotFound, msg).WithHint("write what you still need as a new row (a new id)"),
		}
	}
	msg := g.id + " was deleted from " + g.place
	if g.day != "" {
		msg += " on " + g.day
	}
	return errs.New(errs.NotFound, msg).
		WithHint("`lore trash list --store " + g.place + "` lists what that folder can still restore; a moved row is gone from it")
}

// purgedGoneErr is a purged row's E_PURGED answer (goneRow.err) with the
// same message as E_NOT_FOUND, for a write whose declared errors hold no
// E_PURGED (actor contact remove declares E_NOT_FOUND only): App.Run
// answers it with the code the write declares (purgedAsDeclared), the
// message still saying it was purged. A read always answers E_PURGED, as
// task show of a purged id does on every path.
type purgedGoneErr struct {
	purged, notFound *errs.Error
}

// Error is the E_PURGED error's text.
func (p *purgedGoneErr) Error() string { return p.purged.Error() }

// Unwrap lets errs.Has and errs.EnvelopeOf see the E_PURGED error.
func (p *purgedGoneErr) Unwrap() error { return p.purged }

// purgedAsDeclared is err, or its E_NOT_FOUND twin when err is a purged
// row's answer and e is a write that declares no E_PURGED.
func purgedAsDeclared(e op.Entry, err error) error {
	var p *purgedGoneErr
	if !errors.As(err, &p) || e.Spec().Access != op.AccessWrite || slices.Contains(e.Spec().Errors, errs.Purged) {
		return err
	}
	return p.notFound
}

// storedRowID reports whether id is a well-formed id of a built-in table
// lore keeps in a store's folders (memories, rules, tasks, …): the ids
// an id lookup can say "in no store" about.
func storedRowID(id string) bool {
	if _, err := ids.ParseAny(id); err != nil {
		return false
	}
	p, _, _ := strings.Cut(id, "_")
	for _, t := range schema.Builtin() {
		if t.Kind.IsZero() && t.Synced && string(t.Prefix) == p {
			return true
		}
	}
	return false
}

// noStoreHoldsErr is E_NOT_FOUND for an id no store clone on this
// machine holds (devco:D-589): the places lore looked in, and the one way
// a row it has not seen yet arrives. It names no flag of the command.
func noStoreHoldsErr(id string, looked []string) error {
	sort.Strings(looked)
	msg := id + " is in no store of any setup on this machine"
	if len(looked) > 0 {
		msg += "; looked in " + strings.Join(looked, ", ")
	}
	return errs.New(errs.NotFound, msg).
		WithHint("check the id; a row a teammate wrote arrives once they push it and you run `lore sync --setup <setup>` (`lore setup list` names your setups)")
}

// goneRowAnswer is err, or the deleted-row answer when err is a not-found
// of the row id e names and the org's store clones on this machine hold
// only its tombstone (devco:D-590). Every read path ends here (a repo's
// own, a store clone's folder, the no-project and personal reads), so a
// purged or moved row says where and when it went, whichever path ran.
func (a *App) goneRowAnswer(ctx context.Context, e op.Entry, d op.Decoder, err error) error {
	var le *errs.Error
	if err == nil || !errors.As(err, &le) || le.Code != errs.NotFound {
		return err
	}
	in, derr := e.Decode(d)
	if derr != nil {
		return err
	}
	id := rowIDOf(in)
	if id == "" || !strings.Contains(le.Message, id) || !storedRowID(id) {
		return err
	}
	_, live, look := a.rowLookupOutside(ctx, in)
	switch {
	case look == nil:
	case !live && look.gone != nil:
		return look.gone.err()
	case live && look.livePath != "":
		// a trashed row of a folder the read did not attach answers as a
		// trashed row of an attached one does (E_IN_TRASH)
		if r, ok := trashedRowAt(look.livePath); ok {
			return trash.InTrashErr(r)
		}
	}
	return err
}

// trashedRowAt is the row file at path when it is a row in the trash.
func trashedRowAt(path string) (row.Row, bool) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return row.Row{}, false
	}
	r, err := row.Decode(b)
	if err != nil || !trash.Is(r) {
		return row.Row{}, false
	}
	return r, true
}
