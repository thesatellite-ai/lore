package wire

// idfolder.go — a row named by id that lives in a folder of the org's
// stores this repo does not attach (B2-21). `lore task add … --to
// acme/sync` writes there and attaches the folder for that one command
// only; `lore task show/edit <id>` from the same repo then answered
// E_NOT_FOUND with "it may have landed on main". Now an operation whose
// argument is a row id the attached stores do not hold looks for the row
// file in the folders of the org's store clones on this machine, and
// attaches the folder that holds it for this command, as the --to write
// did; not found there either, the answer says where lore looked.
//
//	op with <id> ─▶ attached stores hold it ─▶ nothing to do
//	             └▶ org store clones: <clone>/<folder>/<table dir>/<id>.json
//	                  ─▶ found: attach "<org>/<folder>" (unrecorded) + a note
//	                  └▶ none: E_NOT_FOUND names the places searched

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// rowFileExt is a row file's extension.
const rowFileExt = ".json"

// idArg is the row id an operation's first positional argument holds, ""
// when it takes none or the value is no row id.
func idArg(e op.Entry, d op.Decoder) string {
	in, err := e.Decode(d)
	if err != nil {
		return ""
	}
	arg0 := ""
	for _, f := range e.InputFields() {
		if f.Arg == 0 {
			arg0 = f.Name
		}
	}
	if arg0 == "" {
		return ""
	}
	for _, tv := range in.Texts() {
		if tv.Field == arg0 {
			if _, perr := ids.ParseAny(tv.Text); perr == nil {
				return tv.Text
			}
			return ""
		}
	}
	return ""
}

// idSearch is what attachFolderOfID looked at for a missing id.
type idSearch struct {
	id       string
	attached []string // the stores attached here
	folders  []string // "<org>/<folder>" of the org's store clones read
}

// attachFolderOfID is the B2-21 step (see the file comment); the returned
// search is non-nil when the id was not found anywhere, for the hint.
func (a *App) attachFolderOfID(ctx context.Context, e op.Entry, d op.Decoder) (*idSearch, error) {
	id := idArg(e, d)
	if id == "" {
		return nil, nil
	}
	anyID, err := ids.ParseAny(id)
	if err != nil {
		return nil, nil
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, nil // the operation reports the open error itself
	}
	if _, _, gerr := eng.Get(ctx, engine.Ref{ID: anyID}); gerr == nil || !errs.Has(gerr, errs.NotFound) {
		return nil, nil
	}
	search := &idSearch{id: id}
	for _, s := range eng.Stores() {
		search.attached = append(search.attached, string(s))
	}
	scan := a.orgFolders(ctx)
	for _, f := range scan.Folders {
		name := f.Org
		if name == "" {
			name = f.Label
		}
		target := name + targetSep + f.Folder
		search.folders = append(search.folders, target)
		dir, derr := a.storeDir(ctx, attach.Store{Name: store.Source(f.Label), URL: f.URL, Branch: f.Branch})
		if derr != nil {
			continue
		}
		hits, _ := filepath.Glob(filepath.Join(dir, f.Folder, "*", id+rowFileExt))
		if len(hits) == 0 {
			continue
		}
		a.attachExtra(attach.Store{Name: store.Source(target), URL: f.URL, Folder: f.Folder, Branch: f.Branch})
		_, _ = fmt.Fprintf(os.Stderr, "lore: %s is in %s, which this repo does not attach; read from there for this command (lore store add … --folder %s attaches it for good)\n", id, target, f.Folder)
		return nil, nil
	}
	return search, nil
}

// attachExtra attaches target for the rest of this process (not recorded
// in stores.yaml) and makes the next Engine call reopen with it.
func (a *App) attachExtra(target attach.Store) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extra = append(a.extra, target)
	if a.st != nil {
		_ = a.st.close() // reopened below with the new target; a close error has nothing left to lose
		a.st, a.opened, a.err = nil, false, nil
	} else if a.opened {
		a.opened, a.err = false, nil
	}
}

// withSearchHint makes a not-found answer for s.id say where lore looked
// (the stores attached here and the org's store folders it read). It goes
// into the message, not the hint: the live hook still explains a deleted
// or purged id, and adds its own next step when nothing else did.
func (s *idSearch) withSearchHint(err error) error {
	var e *errs.Error
	if s == nil || !errors.As(err, &e) || e.Code != errs.NotFound || !strings.Contains(e.Message, s.id) {
		return err
	}
	sort.Strings(s.attached)
	sort.Strings(s.folders)
	where := "looked in the stores attached here (" + strings.Join(s.attached, ", ") + ")"
	if len(s.folders) > 0 {
		where += " and in the org's store folders on this machine (" + strings.Join(s.folders, ", ") + ")"
	}
	c := *e
	c.Message = e.Message + "; " + where
	return &c
}
