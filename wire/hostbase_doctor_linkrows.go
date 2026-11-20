package wire

// hostbase_doctor_linkrows.go — `lore doctor` on a linked repo whose notes
// all moved to its store folder (devco:D-736). A move before D-731 (or an
// adopt, a hand copy) left rows of no use in .lore/data: actor rows the
// folder holds, tombstones of rows the folder holds, comments, and the
// repo's policies row. They reach the repo's pull requests, and an owner
// saw 4, 1 and 2 of them in three repos. Doctor names them with the count
// and `lore doctor --fix` removes every one the linked folder holds:
//
//	linked own repo, no live note left (else the D-729 warning applies)
//	  ─▶ each row file of .lore/data:
//	     _meta.json                 kept: the repo's identity and format;
//	                                the store's link names the repo by its
//	                                project_id, and its repo_id is the
//	                                repo's address history key
//	     projects/<_meta project>,  kept: the repo's identity. Project()
//	     repos/<_meta repo_id>      answers "run lore init" without the
//	                                project row, and sync (EnsureIdentity)
//	                                writes the repos row back from
//	                                _meta.json when its file is gone
//	     policies/<id>              the folder has no live policy of its
//	                                key and scope: moved there (same id);
//	                                the folder's has the same value: the
//	                                repo copy goes; another value: kept,
//	                                and doctor says so (never overwritten)
//	     any other row              the folder holds it (by id; an actor's
//	                                id is its stable key, GlobalKey, and
//	                                any store the repo reads may hold it):
//	                                the repo copy goes; else kept, named
//	                                with the one command that moves it
//	  ─▶ removals recorded as moved out (movedout.go, D-710), so the
//	     repo's pre-commit commits them as lore's own diff

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// doctorLinkedRowsWarning is the plain doctor line; %[1]s counts the rows,
// %[2]s is the linked folder.
const doctorLinkedRowsWarning = "this repo is linked to %[2]s and holds no notes of its own, but its .lore/data still holds %[1]s the folder already holds or that belong there (actors, comments, deletion records, policies); they reach the repo's pull requests. Remove them (the policies the folder lacks move there) with: lore doctor --fix"

// policy row fields compared with the folder's.
const (
	policyKeyField   = "key"
	policyScopeField = "scope"
	policyValueField = "value"
)

// linkedRowFile is one row file of a linked repo's .lore/data.
type linkedRowFile struct {
	Key store.Key
	// Deleted: the file is a tombstone (a deletion record).
	Deleted bool
	// Policy key, scope and value (policies rows only).
	PolicyKey, PolicyScope string
	PolicyValue            value.Value
}

// path is f's "<table>/<id>", the order doctor names rows in.
func (f linkedRowFile) path() string { return string(f.Key.Table) + "/" + f.Key.ID.String() }

// linkedRowFacts is what the linked folder holds, asked per repo row by
// planLinkedRows (a seam: the unit test answers without an engine).
type linkedRowFacts struct {
	// Holds reports whether the folder holds row k: live, or in any state
	// when anyState (a repo tombstone needs only the folder to know the id).
	Holds func(k store.Key, anyState bool) bool
	// FolderPolicy is the folder's live policy value of key and scope; ok
	// is false when the folder sets none.
	FolderPolicy func(key, scope string) (value.Value, bool)
}

// linkedRowPlan is what doctor --fix does in a linked repo.
type linkedRowPlan struct {
	// Remove are the rows the folder holds: their repo files go.
	Remove []store.Key
	// MovePolicies are the policies rows the folder lacks: they move there.
	MovePolicies []store.Key
	// Kept names each row that stays and why (with the command that moves
	// it when there is one).
	Kept []string
}

// count is how many rows the fix removes or moves.
func (p linkedRowPlan) count() int { return len(p.Remove) + len(p.MovePolicies) }

// planLinkedRows decides each row of files (see the file comment). metaProject
// and metaRepo are the ids _meta.json names (the repo's identity rows);
// target is the linked folder's --to.
func planLinkedRows(files []linkedRowFile, metaProject, metaRepo, target string, f linkedRowFacts) linkedRowPlan {
	var p linkedRowPlan
	for _, rf := range files {
		k := rf.Key
		id := k.ID.String()
		switch {
		case k.Table == schema.TableProjects && id == metaProject,
			k.Table == schema.TableRepos && id == metaRepo:
			continue // the repo's identity (file comment)
		case k.Table == schema.TablePolicies && !rf.Deleted:
			if f.Holds(k, false) {
				p.Remove = append(p.Remove, k)
				continue
			}
			have, ok := f.FolderPolicy(rf.PolicyKey, rf.PolicyScope)
			switch {
			case !ok:
				p.MovePolicies = append(p.MovePolicies, k)
			case samePolicyValue(have, rf.PolicyValue):
				p.Remove = append(p.Remove, k)
			default:
				p.Kept = append(p.Kept, "policies/"+id+" ("+rf.PolicyKey+") stays: "+target+" sets "+rf.PolicyKey+" to another value, and lore never overwrites a folder's policy; `lore policy show` shows the one in force")
			}
		case f.Holds(k, rf.Deleted):
			p.Remove = append(p.Remove, k)
		case rf.Deleted:
			p.Kept = append(p.Kept, string(k.Table)+"/"+id+" stays: a deletion record "+target+" has no copy of")
		default:
			p.Kept = append(p.Kept, string(k.Table)+"/"+id+" stays: "+target+" does not hold it; move it there with: lore move --id "+id+" --to "+target+" --yes")
		}
	}
	return p
}

// samePolicyValue reports whether two policy values hold the same JSON
// (a value decodes as JSON or as Text holding it: value.JSONForm gives one
// answer for both).
func samePolicyValue(a, b value.Value) bool {
	ra, oka := value.JSONForm(a)
	rb, okb := value.JSONForm(b)
	return oka && okb && string(ra) == string(rb)
}

// readLinkedRowFiles reads every row file of the repo's .lore/data at top
// (not _meta.json, not OS junk). A file that does not decode is skipped:
// `lore doctor`'s row-format check names it.
func readLinkedRowFiles(top string) ([]linkedRowFile, error) {
	data := filepath.Join(top, filepath.FromSlash(dataDirRel))
	var out []linkedRowFile
	werr := filepath.WalkDir(data, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == rowfs.MetaFileName || rowfs.IsOSJunk(d.Name()) {
			return err
		}
		rel, rerr := filepath.Rel(data, p)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // a row file under the repo's .lore/data
		if rerr != nil {
			return errs.FileIO("read a row file", p, rerr)
		}
		sr, derr := rowfs.DecodeRowFile(filepath.ToSlash(rel), b)
		if derr != nil {
			return nil
		}
		rf := linkedRowFile{Key: store.Key{Table: sr.Table, ID: sr.ID}, Deleted: sr.Deleted}
		if sr.Table == schema.TablePolicies && !sr.Deleted {
			if r, rerr := row.Decode(sr.Doc); rerr == nil {
				rf.PolicyKey = fieldText(r, policyKeyField)
				rf.PolicyScope = fieldText(r, policyScopeField)
				rf.PolicyValue = r.Fields[policyValueField]
			}
		}
		out = append(out, rf)
		return nil
	})
	if werr != nil && !errors.Is(werr, fs.ErrNotExist) {
		return nil, errs.Wrap(errs.Internal, "read "+data, werr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path() < out[j].path() })
	return out, nil
}

// linkedFolderSource is the name the engine reads the linked folder
// target ("<store>/<folder>") by: the target itself when attached under
// it, else the attachment that covers the folder (an org store attached
// at its linked folder is named by the store alone), found the way a --to
// target is (attachFolderPlace). ok is false when no attachment of this
// command holds the folder.
func (a *App) linkedFolderSource(ctx context.Context, eng *engine.Engine, target string) (store.Source, bool) {
	if _, err := eng.List(ctx, store.Source(target), schema.TablePolicies); err == nil {
		return store.Source(target), true
	}
	base, folderName, cut := strings.Cut(target, targetSep)
	if !cut {
		return "", false
	}
	src, found, _, err := a.baseStore(ctx, base)
	if err != nil || !found {
		return "", false
	}
	if f, other := a.otherOrgStoreFolder(ctx, src, folderName); other {
		src = attach.Store{Name: src.Name, URL: f.URL, Branch: f.Branch, ReadOnly: src.ReadOnly}
	}
	same, covered, err := a.coveringAttachment(ctx, src, folderName)
	if err != nil || !covered {
		return "", false
	}
	return same, true
}

// linkedRowsHere is the plan for the repo this command runs in: ok is
// false when the check does not apply (no own linked repo, notes still
// held: the D-729 warning, or the linked folder not open here). name is
// the folder's --to (what doctor prints), to the engine's name for it.
func (a *App) linkedRowsHere(ctx context.Context, eng *engine.Engine) (top, name string, to store.Source, plan linkedRowPlan, ok bool, err error) {
	dir := a.opts.RepoDir
	if dir == "" || inStore(dir) {
		return "", "", "", plan, false, nil
	}
	if a.opts.RepoLinkedByRemote != nil && a.opts.RepoLinkedByRemote(ctx) != "" {
		return "", "", "", plan, false, nil // no lore files in the repo
	}
	top = a.repoLinkedTop(ctx)
	if top == "" || orgcfg.HoldsNotes(top, isRoutedTable) {
		return "", "", "", plan, false, nil
	}
	name = a.linkedFolderTarget(ctx, top)
	to, open := a.linkedFolderSource(ctx, eng, name)
	if !open {
		return "", "", "", plan, false, nil // the folder is not open in this command
	}
	folderPolicies, lerr := eng.List(ctx, to, schema.TablePolicies)
	if lerr != nil {
		return "", "", "", plan, false, nil
	}
	files, err := readLinkedRowFiles(top)
	if err != nil {
		return "", "", "", plan, false, err
	}
	metaProject, _ := repoMetaProjectID(top)
	metaRepo := ""
	if m, found, merr := readJSONFields(filepath.Join(top, filepath.FromSlash(dataDirRel), rowfs.MetaFileName)); merr == nil && found {
		if v := refValues(m["repo_id"]); len(v) == 1 {
			metaRepo = v[0]
		}
	}
	facts := linkedRowFacts{
		Holds: func(k store.Key, anyState bool) bool {
			holds := func(in store.Source) bool {
				src, r, gerr := eng.Get(ctx, engine.Ref{Store: in, Table: k.Table, ID: k.ID})
				return gerr == nil && src == in && (anyState || !r.Deleted)
			}
			if holds(to) {
				return true
			}
			if k.Table != schema.TableActors {
				return false
			}
			// an actor row lives where its org keeps actors (the company
			// folder, D-486), not always in the linked folder: any store
			// this repo reads that holds the actor will do
			for _, s := range eng.Stores() {
				if s != store.SourceRepo && s != store.SourceMachine && s != to && holds(s) {
					return true
				}
			}
			return false
		},
		FolderPolicy: func(key, scope string) (value.Value, bool) {
			for _, r := range folderPolicies {
				if !r.Deleted && fieldText(r, policyKeyField) == key && fieldText(r, policyScopeField) == scope {
					return r.Fields[policyValueField], true
				}
			}
			return nil, false
		},
	}
	return top, name, to, planLinkedRows(files, metaProject, metaRepo, name, facts), true, nil
}

// doctorLinkedRows is the D-736 check: warn (plain doctor) or remove and
// move (--fix) the rows a linked repo with no notes left does not need.
func (a *App) doctorLinkedRows(ctx context.Context, fix bool, warn, fixed func(string)) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return
	}
	top, name, to, plan, ok, err := a.linkedRowsHere(ctx, eng)
	if err != nil {
		warn("could not read this repo's .lore/data: " + err.Error())
		return
	}
	if !ok {
		return
	}
	for _, k := range plan.Kept {
		warn(k)
	}
	if plan.count() == 0 {
		return
	}
	if !fix {
		warn(fmt.Sprintf(doctorLinkedRowsWarning, plural(plan.count(), "row"), name))
		return
	}
	line, err := a.fixLinkedRows(ctx, top, name, to, plan)
	if err != nil {
		warn("lore doctor --fix could not clean this repo's .lore/data: " + err.Error() + "; run lore doctor --fix again")
		return
	}
	fixed(line)
}

// fixLinkedRows applies plan: each policy the folder lacks is written
// there under its own id (key, value, scope), then its repo copy goes with
// the rows the folder holds, recorded as moved out. Not a `lore move`: a
// move writes a tombstone into the repo, whose actor stamp would put the
// actor row back that this fix removes. It returns the line doctor prints.
func (a *App) fixLinkedRows(ctx context.Context, top, name string, to store.Source, plan linkedRowPlan) (string, error) {
	var said []string
	remove := append([]store.Key(nil), plan.Remove...)
	if len(plan.MovePolicies) > 0 {
		eng, err := a.Engine(ctx)
		if err != nil {
			return "", err
		}
		for _, k := range plan.MovePolicies {
			_, r, gerr := eng.Get(ctx, engine.Ref{Store: store.SourceRepo, Table: k.Table, ID: k.ID})
			if gerr != nil {
				return "", errs.Prefix("read policies/"+k.ID.String(), gerr)
			}
			fields := map[string]value.Value{policyKeyField: r.Fields[policyKeyField], policyScopeField: r.Fields[policyScopeField]}
			raw, _ := value.JSONForm(r.Fields[policyValueField])
			if fields[policyValueField], err = value.JSONOf(raw); err != nil {
				return "", errs.Prefix("read the value of policies/"+k.ID.String(), err)
			}
			if _, cerr := a.CreateWithID(ctx, to, k.Table, k.ID, fields); cerr != nil {
				return "", errs.Prefix("write policies/"+k.ID.String()+" to "+name, cerr)
			}
		}
		remove = append(remove, plan.MovePolicies...)
		said = append(said, "moved "+plural(len(plan.MovePolicies), "policy row")+" to "+name+" (it had none of their keys)")
	}
	if _, _, err := a.adoptLiveRemove(ctx, top, remove); err != nil {
		return "", err
	}
	removed, err := removeAdopted(top, remove)
	if err != nil {
		return "", err
	}
	pruneEmptyDirs(filepath.Join(top, filepath.FromSlash(dataDirRel)))
	if err := RecordMovedOut(ctx, top, name, removed); err != nil {
		return "", err
	}
	if n := len(removed) - len(plan.MovePolicies); n > 0 {
		said = append(said, "removed "+plural(n, "row")+" "+name+" holds")
	}
	return "this linked repo's .lore/data: " + strings.Join(said, "; ") + "; commit the change (git add -A .lore/data)", nil
}
