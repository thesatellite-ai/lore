package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
)

// LandAction is what ShouldLand decides for one live row (F5 "when").
type LandAction string

const (
	// LandStay means nothing to do now (the reason says why).
	LandStay LandAction = "stay"
	// LandFinal writes the row to main; it leaves live once main is seen
	// to hold the same content (Confirm).
	LandFinal LandAction = "land"
	// LandCopy writes the row to main but keeps it live (an open row named
	// by `lore land`, or at its max age: rule 6c).
	LandCopy LandAction = "copy"
)

// LandInputs are what ShouldLand decides from.
type LandInputs struct {
	File File
	// Finished is the kind's finished test applied to the body (task
	// done/cancelled, …; rules, memories, notes and comments are finished on
	// every write: TEAM-027). A tombstone is always finished.
	Finished bool
	// Named: `lore land` named this row (one id, --all, --kind, --mission).
	Named bool
	Now   time.Time
	// MaxAge is live.max_age.
	MaxAge time.Duration
	// MainBody is the row's canonical body on the fetched default branch;
	// nil when main has no file for it.
	MainBody []byte
	// MainPurged: main's _purged.json lists the id.
	MainPurged bool
}

// LandDecision is ShouldLand's answer with the reason `lore land --dry-run`
// and `lore explain` print.
type LandDecision struct {
	Action LandAction
	Reason string
}

// Reasons, as printed.
const (
	reasonConflict     = "conflict markers: settle it with `lore sync resolve` first"
	reasonOnMain       = "main already holds this version"
	reasonFinished     = "finished"
	reasonNamedOpen    = "open: copied, stays live"
	reasonNamedDone    = "named by lore land"
	reasonMaxAge       = "open for longer than live.max_age: copied, stays live"
	reasonOpenYoung    = "open, younger than live.max_age"
	reasonTombstone    = "deleted: lands as a file delete and a _purged.json id"
	reasonTombstoneOld = "deleted and already removed from main"
)

// ShouldLand is F5's decision for one row (rule 6). It never lands a row
// with conflict markers, and never lands what main already holds (landing
// twice is a no-op).
func ShouldLand(in LandInputs) LandDecision {
	f := in.File
	if f.Deleted() {
		if in.MainBody == nil && in.MainPurged {
			return LandDecision{LandStay, reasonTombstoneOld}
		}
		return LandDecision{LandFinal, reasonTombstone}
	}
	if f.Block.Conflict || f.HasConflictMarkers() {
		return LandDecision{LandStay, reasonConflict}
	}
	body, err := f.EncodeBody()
	if err == nil && in.MainBody != nil && sameBytes(body, in.MainBody) {
		return LandDecision{LandStay, reasonOnMain}
	}
	switch {
	case in.Finished && in.Named:
		return LandDecision{LandFinal, reasonNamedDone}
	case in.Finished:
		return LandDecision{LandFinal, reasonFinished}
	case in.Named:
		return LandDecision{LandCopy, reasonNamedOpen}
	}
	since := f.Block.LastCopyAt
	if since.IsZero() {
		since = f.Block.LiveSince
	}
	if !since.IsZero() && in.Now.Sub(since) >= in.MaxAge {
		return LandDecision{LandCopy, reasonMaxAge}
	}
	return LandDecision{LandStay, reasonOpenYoung}
}

// ConfirmInputs are what Confirm decides from.
type ConfirmInputs struct {
	File     File
	Finished bool
	Now      time.Time
	MaxAge   time.Duration
	// MainBody / MainPurged as in LandInputs, read from a freshly fetched
	// default branch.
	MainBody   []byte
	MainPurged bool
}

// Confirm is F5 "leaving live": a row leaves live only when it is finished
// and main holds exactly its content. A tombstone leaves only after main
// deleted the file and purged the id, and at least max_age after the
// delete, so an old branch copy cannot come back meanwhile (side effect 11).
func Confirm(in ConfirmInputs) bool {
	f := in.File
	if f.Deleted() {
		at := f.Block.HLC.Wall
		return in.MainBody == nil && in.MainPurged && !at.IsZero() && in.Now.Sub(at) >= in.MaxAge
	}
	if !in.Finished || in.MainBody == nil || f.Block.Conflict {
		return false
	}
	body, err := f.EncodeBody()
	return err == nil && sameBytes(body, in.MainBody)
}

func sameBytes(a, b []byte) bool {
	return strings.TrimSpace(string(a)) == strings.TrimSpace(string(b))
}

// ---------------------------------------------------------------- executor

// LandItem is one row to write to main.
type LandItem struct {
	Folder string
	Key    RowKey
	File   File
	Action LandAction
}

// Layout maps live folders to paths on the default branch. Lore's layout is
// <project>/.lore/data/<table>/<id>.json; the caller passes it so this
// package names no project layout itself.
type Layout struct {
	// DataPath returns the branch path of rel ("<table>/<id>.json" or
	// "_purged.json") for a folder.
	DataPath func(folder, rel string) string
	// AddPurged returns new _purged.json bytes: existing (nil when absent)
	// plus ids, in lore's canonical format.
	AddPurged func(existing []byte, ids []string) ([]byte, error)
	// BinaryVersion, when set, is this lore's version: BuildLanding then
	// refuses (E_OLD_LORE) to write a folder whose branch _meta.json this
	// binary may not write (FORMAT_EVOLUTION rule 2 "read-only on every
	// path": a landing or ride-along commit is a write to the branch's
	// folder, so it asks the same formatgate.Check as an engine write).
	// Empty skips the check (callers that predate it).
	BinaryVersion string
}

// branchMetaFile is a folder's format declaration on the branch, beside
// its rows (rowfs MetaFileName).
const branchMetaFile = "_meta.json"

// branchFormat is the declaration part of a branch folder's _meta.json
// (rowfs MetaKeyFeatures, MetaKeyMinLore: file format, must not change).
type branchFormat struct {
	Features       []string `json:"features"`
	MinLoreVersion string   `json:"min_lore_version"`
}

// checkBranchFormat refuses a landing into a folder whose branch
// _meta.json this binary may not write. A missing file declares nothing;
// one that does not decode (conflict markers, a hand edit) fails closed,
// as rowfs does for the checkout's copy.
func checkBranchFormat(read func(path string) ([]byte, bool, error), layout Layout, folders []string) error {
	if layout.BinaryVersion == "" {
		return nil
	}
	for _, f := range folders {
		p := layout.DataPath(f, branchMetaFile)
		b, has, err := read(p)
		if err != nil {
			return err
		}
		if !has {
			continue
		}
		var d branchFormat
		if err := json.Unmarshal(b, &d); err != nil {
			return formatgate.Unreadable(p, "not a JSON object with features and min_lore_version")
		}
		v := formatgate.Check(layout.BinaryVersion, formatgate.Declared{Features: d.Features, MinLoreVersion: d.MinLoreVersion})
		if gerr := v.Err(); gerr != nil {
			return errs.Wrap(errs.OldLore, "live: landing into "+p, gerr).WithHint("lore upgrade")
		}
	}
	return nil
}

// purgedFile is the name of the purged-id list inside .lore/data.
const purgedFile = "_purged.json"

// LandStatus is the outcome of Land.
type LandStatus string

// Land outcomes.
const (
	LandNothing    LandStatus = "nothing"     // main already held every row
	LandDone       LandStatus = "landed"      // pushed to main
	LandProtected  LandStatus = "protected"   // main refused direct pushes: ride-along (F6)
	LandOffline    LandStatus = "offline"     // no verdict
	LandNoRemote   LandStatus = "no-remote"   // nowhere to land: no remote, or no default branch on it yet
	LandRetriesOut LandStatus = "retries-out" // main kept moving
	LandRefused    LandStatus = "refused"     // any other refusal
)

// LandResult reports a Land.
type LandResult struct {
	Status LandStatus
	Commit string
	Rows   int
	Detail string
}

// LandRequest lands rows on the default branch of Ref's remote.
type LandRequest struct {
	Branch string
	Items  []LandItem
	Layout Layout
}

// headsPrefix is the branch ref namespace.
const headsPrefix = "refs/heads/"

// Land runs F5 "how": fetch the default branch, build ONE commit on the
// fetched tip with every row (tombstones as a delete plus a _purged.json
// id), and push it as <sha>:refs/heads/<branch> without force. Local
// branches are never read or moved, so a user's unpushed commits on main
// are never pushed. A non-fast-forward refetches and rebuilds; a
// protection refusal is reported for the ride-along.
func (r *Ref) Land(ctx context.Context, req LandRequest) (LandResult, error) {
	if r.Remote == nil {
		return LandResult{Status: LandNoRemote}, nil
	}
	if len(req.Items) == 0 {
		return LandResult{Status: LandNothing}, nil
	}
	dst := headsPrefix + req.Branch
	trk, _ := gitremote.TrackingRef(r.remoteName(), dst)
	for attempt := 1; attempt <= r.PushRetries+1; attempt++ {
		if err := r.Remote.Fetch(ctx, gitremote.FetchRequest{Refspecs: gitremote.TrackingRefspecs(r.remoteName(), req.Branch)}); err != nil {
			if errors.Is(err, gitremote.ErrUnconfirmed) {
				return LandResult{Status: LandOffline}, nil
			}
			if errors.Is(err, gitremote.ErrRemoteRefMissing) {
				// a brand-new repo whose remote has no default branch yet:
				// nothing to land on, the rows stay live (U-2)
				return LandResult{Status: LandNoRemote}, nil
			}
			return LandResult{}, err
		}
		tip, ok, err := r.Repo.ResolveRef(ctx, trk)
		if err != nil {
			return LandResult{}, err
		}
		if !ok {
			return LandResult{Status: LandNoRemote}, nil // as above: no default branch on the remote yet
		}
		ch, changed, err := r.landChanges(ctx, tip, req)
		if err != nil {
			return LandResult{}, err
		}
		if ch.Empty() {
			return LandResult{Status: LandNothing}, nil
		}
		n := len(changed)
		c, err := r.Repo.MakeCommit(ctx, gitref.CommitRequest{
			Base: tip, Changes: ch, Message: LandMessage(changed), Signature: r.sig(),
		})
		if err != nil {
			return LandResult{}, err
		}
		if !c.Changed {
			return LandResult{Status: LandNothing}, nil
		}
		res, err := r.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: c.OID, Dst: dst, Mode: gitremote.ModeFastForward}}})
		if err != nil {
			if errors.Is(err, gitremote.ErrUnconfirmed) {
				return LandResult{Status: LandOffline}, nil
			}
			return LandResult{}, err
		}
		rr, _ := res.Ref(dst)
		switch {
		case rr.Status.Applied():
			if cur, _, err := r.Repo.ResolveRef(ctx, trk); err == nil && cur != c.OID {
				_ = r.Repo.UpdateRef(ctx, trk, c.OID, cur) // tracking catches up; the next fetch would too
			}
			return LandResult{Status: LandDone, Commit: c.OID, Rows: n}, nil
		case rr.Status == gitremote.StatusProtected:
			return LandResult{Status: LandProtected, Detail: rr.Message, Rows: n}, nil
		case rr.Status == gitremote.StatusNonFastForward || rr.Status == gitremote.StatusStale:
			continue
		default:
			return LandResult{Status: LandRefused, Detail: rr.Message}, nil
		}
	}
	return LandResult{Status: LandRetriesOut}, nil
}

// landChanges builds the file changes of a landing on base and the items
// that change main.
func (r *Ref) landChanges(ctx context.Context, base string, req LandRequest) (gitref.Changes, []LandItem, error) {
	return BuildLanding(ctx, func(p string) ([]byte, bool, error) { return r.Repo.ReadFile(ctx, base, p) }, req.Items, req.Layout)
}

// BuildLanding computes the changes that put items onto a branch whose
// files read returns (the landing commit and the ride-along commit share
// it), and the items that change the branch. Rows whose content the branch
// already holds are left out.
func BuildLanding(_ context.Context, read func(path string) ([]byte, bool, error), items []LandItem, layout Layout) (gitref.Changes, []LandItem, error) {
	ch := gitref.Changes{Put: map[string][]byte{}}
	if err := checkBranchFormat(read, layout, landFolders(items)); err != nil {
		return ch, nil, err
	}
	purge := map[string][]string{} // folder → ids
	// purgeOnly are tombstones whose file main never had: they change main
	// only if their id is new in _purged.json.
	purgeOnly := map[string][]LandItem{}
	var changed []LandItem
	for _, it := range items {
		p := layout.DataPath(it.Folder, it.Key.Table+"/"+it.Key.ID+rowExt)
		cur, has, err := read(p)
		if err != nil {
			return ch, nil, err
		}
		if it.File.Deleted() {
			if has {
				ch.Delete = append(ch.Delete, p)
				changed = append(changed, it)
			} else {
				purgeOnly[it.Folder] = append(purgeOnly[it.Folder], it)
			}
			purge[it.Folder] = append(purge[it.Folder], it.Key.ID)
			continue
		}
		body, err := it.File.EncodeBody()
		if err != nil {
			return ch, nil, err
		}
		if has && sameBytes(cur, body) {
			continue
		}
		ch.Put[p] = body
		changed = append(changed, it)
	}
	folders := make([]string, 0, len(purge))
	for f := range purge {
		folders = append(folders, f)
	}
	sort.Strings(folders)
	for _, f := range folders {
		p := layout.DataPath(f, purgedFile)
		cur, has, err := read(p)
		if err != nil {
			return ch, nil, err
		}
		var existing []byte
		if has {
			existing = cur
		}
		b, err := layout.AddPurged(existing, purge[f])
		if err != nil {
			return ch, nil, err
		}
		if !has || !sameBytes(cur, b) {
			ch.Put[p] = b
			changed = append(changed, purgeOnly[f]...)
		}
	}
	sort.Strings(ch.Delete)
	return ch, changed, nil
}

// landFolders lists the distinct folders of items, sorted.
func landFolders(items []LandItem) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		if !seen[it.Folder] {
			seen[it.Folder] = true
			out = append(out, it.Folder)
		}
	}
	sort.Strings(out)
	return out
}

// LandMessage is the fixed message of a landing or ride-along commit (F5
// step 4): "lore: land N rows (<folders>)" and the ids, one per line.
func LandMessage(items []LandItem) string {
	folders := map[string]bool{}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		folders[it.Folder] = true
		ids = append(ids, it.Key.ID)
	}
	names := make([]string, 0, len(folders))
	for f := range folders {
		names = append(names, f)
	}
	sort.Strings(names)
	sort.Strings(ids)
	return fmt.Sprintf("lore: land %d %s (%s)\n\n%s\n", len(items), plural(len(items), "row", "rows"), strings.Join(names, ", "), strings.Join(ids, "\n"))
}
