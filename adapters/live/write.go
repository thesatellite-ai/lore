package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/formatgate"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
)

// NewVersion builds the live file for a new or edited row body (F2): the
// row keeps its live_since and last_copy_at, and gets an hlc strictly above
// the version it edits.
func NewVersion(prev *File, body canonjson.Object, now time.Time, actor string) File {
	var b Block
	if prev != nil {
		b = prev.Block
		b.Deleted = false
		b.Companion = false // an edit is a write on purpose
	}
	if b.LiveSince.IsZero() {
		b.LiveSince = now.UTC().Truncate(time.Second)
	}
	var prevHLC HLC
	if prev != nil {
		prevHLC = prev.Block.HLC
	}
	b.HLC = NextHLC(prevHLC, now, actor)
	f := File{Body: body, Block: b}
	f.Block.Conflict = f.HasConflictMarkers()
	return f
}

// NewTombstone builds the tombstone for a deleted row (§3.3).
func NewTombstone(prev *File, id string, now time.Time, actor string) File {
	f := NewVersion(prev, canonjson.Object{idKey: canonjson.String(id)}, now, actor)
	return Tombstone(id, f.Block)
}

// WriteRequest is one local live write (F2 steps 2–5).
type WriteRequest struct {
	// Folder is the project folder the rows belong to.
	Folder string
	// Rows are new versions (NewVersion / NewTombstone). A row whose body
	// equals the current live body (IgnoreKeys aside) and whose deleted
	// flag matches is skipped, so the same write twice commits nothing.
	Rows map[RowKey]File
	// Exact are files written as given even when only their block differs
	// (a max-age copy's last_copy_at).
	Exact map[RowKey]File
	// Remove are rows that leave live (landed and confirmed, F5).
	Remove []RowKey
	// RemoveIf are rows that leave live only if their live file still holds
	// exactly these bytes (compare-and-swap on the blob, F5: if the row
	// changed meanwhile, it stays).
	RemoveIf map[RowKey][]byte
	// Raw are other paths to write as given (the ref's _live/ files).
	Raw map[string][]byte
	// RawDelete are other paths to remove.
	RawDelete []string
	// BinaryVersion is the running lore's version, checked against the
	// ref's min_lore_version.
	BinaryVersion string
}

// WriteResult is the outcome of Write.
type WriteResult struct {
	Old, New string
	Changed  bool
	// Written lists the rows that changed in the commit.
	Written []RowKey
}

// casRetries bounds re-reads when another local process moved the ref
// between our read and our compare-and-swap.
const casRetries = 5

// Write commits the request on the local live ref with compare-and-swap.
// The first write ever is one parentless commit that also creates
// _live/epoch and _live/meta.json (C2). Nothing is pushed here.
func (r *Ref) Write(ctx context.Context, req WriteRequest) (WriteResult, error) {
	if err := ValidateFolder(req.Folder); err != nil {
		return WriteResult{}, err
	}
	for attempt := 0; ; attempt++ {
		res, err := r.writeOnce(ctx, req)
		if err == nil || !errors.Is(err, gitref.ErrRefMoved) || attempt >= casRetries {
			return res, err
		}
	}
}

func (r *Ref) writeOnce(ctx context.Context, req WriteRequest) (WriteResult, error) {
	st, err := r.Stage(ctx, req)
	if err != nil || st.New == st.Old {
		return WriteResult{Old: st.Old, New: st.Old}, err
	}
	if err := r.Repo.UpdateRef(ctx, r.Name, st.New, st.Old); err != nil {
		return WriteResult{}, err
	}
	if err := r.addPending(st.Changes); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Old: st.Old, New: st.New, Changed: true, Written: st.Written}, nil
}

// Staged is a live commit built on the local ref but not applied: no ref
// moved, nothing recorded as pending. Coordination writes (F7) push it
// atomically with the signals ref and apply it only once the remote
// confirmed both, so a holder who lost its claim never publishes its row
// change through a later ordinary push.
type Staged struct {
	// Old is the local ref the commit builds on ("" = absent); New the
	// staged commit (New == Old: nothing to write).
	Old, New string
	Changes  gitref.Changes
	Written  []RowKey
}

// Update is the push half of a staged write: a plain fast-forward of the
// live ref to New.
func (s Staged) Update(ref string) gitremote.RefUpdate {
	return gitremote.RefUpdate{Src: s.New, Dst: ref, Mode: gitremote.ModeFastForward}
}

// Stage builds the commit Write would make, without moving any ref. Call
// Integrate first so New fast-forwards the remote tip; calling Stage again
// after a lost race re-reads everything, so it is safe to repeat.
func (r *Ref) Stage(ctx context.Context, req WriteRequest) (Staged, error) {
	if err := ValidateFolder(req.Folder); err != nil {
		return Staged{}, err
	}
	old, tree, err := r.LocalTree(ctx)
	if err != nil {
		return Staged{}, err
	}
	ch, written, err := r.changesFor(tree, req)
	if err != nil || ch.Empty() {
		return Staged{Old: old, New: old}, err
	}
	c, err := r.Repo.MakeCommit(ctx, gitref.CommitRequest{
		Base: old, Changes: ch, Message: commitMessage(ch), Signature: r.sig(),
	})
	if err != nil {
		return Staged{}, err
	}
	if !c.Changed {
		return Staged{Old: old, New: old}, nil
	}
	return Staged{Old: old, New: c.OID, Changes: ch, Written: written}, nil
}

// CommitStaged applies a staged write after the remote confirmed New: the
// local ref moves Old→New by compare-and-swap, the tracking ref and the
// seen marker move to New, and the pending list empties (the remote now
// holds everything up to New).
func (r *Ref) CommitStaged(ctx context.Context, s Staged) error {
	if s.New == s.Old {
		return nil
	}
	if err := r.Repo.UpdateRef(ctx, r.Name, s.New, s.Old); err != nil {
		return err
	}
	if err := r.setTracking(ctx, s.New); err != nil {
		return err
	}
	return r.confirmed(ctx, s.New)
}

// changesFor computes a write's tree changes on tree (see WriteRequest).
func (r *Ref) changesFor(tree Tree, req WriteRequest) (gitref.Changes, []RowKey, error) {
	if err := checkFloor(tree, req.BinaryVersion); err != nil {
		return gitref.Changes{}, nil, err
	}
	ch := gitref.Changes{Put: map[string][]byte{}}
	var written []RowKey
	for _, k := range sortedRowKeys(req.Rows) {
		f := req.Rows[k]
		p := RowPath(req.Folder, k.Table, k.ID)
		if cur, ok := tree[p]; ok {
			if same, err := r.sameRow(cur, f); err != nil {
				return gitref.Changes{}, nil, err
			} else if same {
				continue
			}
		}
		b, err := f.Encode()
		if err != nil {
			return gitref.Changes{}, nil, err
		}
		ch.Put[p] = b
		written = append(written, k)
	}
	if err := raiseMeta(tree, ch, req.Rows, written); err != nil {
		return gitref.Changes{}, nil, err
	}
	for _, k := range sortedRowKeys(req.Exact) {
		b, err := req.Exact[k].Encode()
		if err != nil {
			return gitref.Changes{}, nil, err
		}
		p := RowPath(req.Folder, k.Table, k.ID)
		if string(tree[p]) != string(b) {
			ch.Put[p] = b
			written = append(written, k)
		}
	}
	for _, k := range req.Remove {
		p := RowPath(req.Folder, k.Table, k.ID)
		if _, ok := tree[p]; ok {
			ch.Delete = append(ch.Delete, p)
			written = append(written, k)
		}
	}
	for _, k := range sortedRowKeys2(req.RemoveIf) {
		p := RowPath(req.Folder, k.Table, k.ID)
		if cur, ok := tree[p]; ok && string(cur) == string(req.RemoveIf[k]) {
			ch.Delete = append(ch.Delete, p)
			written = append(written, k)
		}
	}
	for p, b := range req.Raw {
		if string(tree[p]) != string(b) {
			ch.Put[p] = b
		}
	}
	for _, p := range req.RawDelete {
		if _, ok := tree[p]; ok {
			ch.Delete = append(ch.Delete, p)
		}
	}
	if ch.Empty() {
		return ch, nil, nil
	}
	if err := addRefFiles(tree, ch); err != nil {
		return gitref.Changes{}, nil, err
	}
	return ch, written, nil
}

// checkFloor refuses a write by a binary that may not write the ref: below
// its min_lore_version, or missing one of its features (FORMAT_EVOLUTION
// rule 2 "read-only on every path": the live ref asks the same
// formatgate.Check as core writes, so FMT-EFF and unknown-feature blocking
// match). The error wraps ErrBelowFloor so the CLI maps it to E_OLD_LORE.
func checkFloor(t Tree, version string) error {
	m, ok, err := t.Meta()
	if err != nil || !ok || version == "" {
		return err
	}
	v := formatgate.Check(version, formatgate.Declared{Features: m.Features, MinLoreVersion: m.MinLoreVersion})
	if gerr := v.Err(); gerr != nil {
		return fmt.Errorf("%w: %s", ErrBelowFloor, gerr.Error())
	}
	return nil
}

// CheckFloor reports ErrBelowFloor when version may not write the local
// live ref (used by commands before they change anything).
func (r *Ref) CheckFloor(ctx context.Context, version string) error {
	_, tree, err := r.LocalTree(ctx)
	if err != nil {
		return err
	}
	return checkFloor(tree, version)
}

// addRefFiles adds _live/epoch and _live/meta.json when the tree lacks
// them (the first write, or a ref made by hand).
func addRefFiles(tree Tree, ch gitref.Changes) error {
	if _, ok := tree[EpochPath]; !ok {
		if _, put := ch.Put[EpochPath]; !put {
			ch.Put[EpochPath] = encodeEpoch(FirstEpoch)
		}
	}
	if _, ok := tree[MetaPath]; !ok {
		if _, put := ch.Put[MetaPath]; !put {
			b, err := DefaultMeta().Encode()
			if err != nil {
				return err
			}
			ch.Put[MetaPath] = b
		}
	}
	return nil
}

// raiseMeta adds the features the written rows use to _live/meta.json in
// the same commit as the rows (FORMAT_EVOLUTION rule 2: "in _live/meta.json
// in the SAME live commit as the row"), raising min_lore_version to their
// Since. Without it a lore that predates a feature passes the floor check
// and rewrites the row without the fields it does not know
// (F-COMPAT-LIVE-1: v0.1.17 dropped parent_id, lead and assignees).
func raiseMeta(tree Tree, ch gitref.Changes, rows map[RowKey]File, written []RowKey) error {
	files := make([]File, 0, len(written))
	for _, k := range written {
		if f, ok := rows[k]; ok {
			files = append(files, f)
		}
	}
	need := rowFeatures(files)
	if len(need) == 0 {
		return nil
	}
	cur, has := ch.Put[MetaPath]
	if !has {
		cur, has = tree[MetaPath]
	}
	if !has {
		cur = nil
	}
	b, changed, err := raiseMetaFile(cur, need)
	if err != nil || !changed {
		return err
	}
	ch.Put[MetaPath] = b
	return nil
}

// sameRow reports whether the stored live file already holds f's body
// (IgnoreKeys aside) with the same deleted flag.
func (r *Ref) sameRow(cur []byte, f File) (bool, error) {
	old, err := DecodeFile(cur)
	if err != nil {
		return false, nil // unreadable: overwrite with a good version
	}
	if old.Deleted() != f.Deleted() {
		return false, nil
	}
	a, b := old, f
	for _, k := range r.IgnoreKeys {
		a.Body, b.Body = stripKey(a.Body, k), stripKey(b.Body, k)
	}
	return SameBody(a, b)
}

func sortedRowKeys(m map[RowKey]File) []RowKey {
	out := make([]RowKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func sortedRowKeys2(m map[RowKey][]byte) []RowKey {
	out := make([]RowKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// ---------------------------------------------------------------- pending

// PendingEntry is one live write not yet confirmed by the remote
// (.git/lore/live-pending.json, F2): the path and the blob it wrote.
type PendingEntry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// Pending reads the pending list.
func (r *Ref) Pending() ([]PendingEntry, error) {
	b, err := os.ReadFile(r.stateFile(pendingSuffix))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("live: read pending: %w", err)
	}
	var v struct {
		Entries []PendingEntry `json:"entries"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("live: pending list: %w", err)
	}
	return v.Entries, nil
}

func (r *Ref) addPending(ch gitref.Changes) error {
	cur, err := r.Pending()
	if err != nil {
		cur = nil // a broken list is rebuilt; the local ref is the real queue
	}
	byPath := map[string]PendingEntry{}
	for _, e := range cur {
		byPath[e.Path] = e
	}
	for p, b := range ch.Put {
		if _, _, ok := ParseRowPath(p); ok {
			byPath[p] = PendingEntry{Path: p, Hash: contentHash(b)}
		}
	}
	for _, p := range ch.Delete {
		if _, _, ok := ParseRowPath(p); ok {
			byPath[p] = PendingEntry{Path: p}
		}
	}
	return r.savePending(byPath)
}

func (r *Ref) savePending(byPath map[string]PendingEntry) error {
	if len(byPath) == 0 {
		err := os.Remove(r.stateFile(pendingSuffix))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("live: clear pending: %w", err)
		}
		return nil
	}
	list := make([]PendingEntry, 0, len(byPath))
	for _, e := range byPath {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	b, err := json.MarshalIndent(struct {
		Entries []PendingEntry `json:"entries"`
	}{list}, "", "  ")
	if err != nil {
		return err
	}
	return writeState(r.stateFile(pendingSuffix), append(b, '\n'))
}

// ---------------------------------------------------------------- push

// PushStatus is the outcome of Push.
type PushStatus string

// Push outcomes.
const (
	PushNothing    PushStatus = "nothing"     // local not ahead
	PushDone       PushStatus = "pushed"      // confirmed by the remote
	PushNoRemote   PushStatus = "no-remote"   // nowhere to push (solo)
	PushOffline    PushStatus = "offline"     // no verdict: remote unreachable
	PushRefused    PushStatus = "refused"     // the host refused refs/lore/* (or protection)
	PushRetriesOut PushStatus = "retries-out" // lost every race
)

// PushOutcome reports a Push.
type PushOutcome struct {
	Status PushStatus
	// Detail is the remote's reason for a refusal, or git's stderr of an
	// offline push (gitfault reads it: no answer, no repository, refused
	// credentials).
	Detail string
	// Notes are merge decisions made while retrying.
	Notes []MergeNote
	// Attempts counts push calls.
	Attempts int
	// Commit is the commit the remote's live ref holds after a push that
	// sent one (PushDone with Attempts > 0); Ref and Remote name where it
	// went, so the command's output can say (no hidden network writes).
	Commit, Ref, Remote string
}

// Pushed reports a confirmed push (or nothing to push).
func (o PushOutcome) Pushed() bool { return o.Status == PushDone || o.Status == PushNothing }

// Push sends the local ref to the remote (F2 step 6): a plain fast-forward
// push; a rejection fetches, merges onto the remote tip (F4) and retries up
// to PushRetries times with backoff. Offline or refused pushes leave the
// commit on the local ref (the queue) and are not errors.
func (r *Ref) Push(ctx context.Context) (PushOutcome, error) {
	if r.Remote == nil {
		return PushOutcome{Status: PushNoRemote}, nil
	}
	var out PushOutcome
	for attempt := 1; attempt <= r.PushRetries+1; attempt++ {
		s, err := r.Snapshot(ctx)
		if err != nil {
			return out, err
		}
		if !s.Ahead() {
			if out.Attempts == 0 {
				out.Status = PushNothing
			} else {
				out.Status = PushDone
			}
			return out, r.confirmed(ctx, s.Local)
		}
		out.Attempts++
		res, err := r.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{
			{Src: s.Local, Dst: r.Name, Mode: gitremote.ModeFastForward},
		}})
		if err != nil {
			if errors.Is(err, gitremote.ErrUnconfirmed) {
				out.Status = PushOffline
				out.Detail = transportStderr(err)
				return out, nil
			}
			return out, err
		}
		rr, _ := res.Ref(r.Name)
		switch {
		case rr.Status.Applied():
			if err := r.setTracking(ctx, s.Local); err != nil {
				return out, err
			}
			out.Status = PushDone
			out.Commit, out.Ref, out.Remote = s.Local, r.Name, r.remoteName()
			return out, r.confirmed(ctx, s.Local)
		case rr.Status == gitremote.StatusNonFastForward || rr.Status == gitremote.StatusStale:
			if err := r.Fetch(ctx); err != nil {
				if errors.Is(err, gitremote.ErrUnconfirmed) {
					out.Status = PushOffline
					out.Detail = transportStderr(err)
					return out, nil
				}
				return out, err
			}
			ir, err := r.Integrate(ctx)
			if err != nil {
				return out, err
			}
			out.Notes = append(out.Notes, ir.Notes...)
			if r.Backoff != nil {
				if d := r.Backoff(attempt); d > 0 {
					select {
					case <-ctx.Done():
						return out, ctx.Err()
					case <-time.After(d):
					}
				}
			}
		default:
			out.Status = PushRefused
			out.Detail = rr.Message
			return out, nil
		}
	}
	out.Status = PushRetriesOut
	return out, nil
}

// confirmed records that the remote holds oid: the seen marker moves and
// the pending list empties.
func (r *Ref) confirmed(ctx context.Context, oid string) error {
	if oid == "" {
		return nil
	}
	s, err := r.Snapshot(ctx)
	if err != nil {
		return err
	}
	if s.Tracking != oid {
		return nil // still ahead (nothing was pushed): keep the list
	}
	if err := r.markSeen(oid); err != nil {
		return err
	}
	return r.savePending(nil)
}

// setTracking moves the tracking ref to what the remote now holds. git push
// to a named remote may already have moved it; either way it ends at oid.
func (r *Ref) setTracking(ctx context.Context, oid string) error {
	trk := r.Tracking()
	if trk == "" {
		return nil
	}
	cur, _, err := r.Repo.ResolveRef(ctx, trk)
	if err != nil {
		return err
	}
	if cur == oid {
		return nil
	}
	return r.Repo.UpdateRef(ctx, trk, oid, cur)
}

// Fetch runs one fetch of every refs/lore/* ref (and the named branches)
// into tracking refs, pruning refs the remote no longer has (F3 step 2).
// It never writes a local refs/lore/* ref.
func (r *Ref) Fetch(ctx context.Context, branches ...string) error {
	if r.Remote == nil {
		return nil
	}
	return r.Remote.Fetch(ctx, gitremote.FetchRequest{
		Refspecs: gitremote.TrackingRefspecs(r.remoteName(), branches...),
		Prune:    true,
	})
}

// ForcePush replaces the remote ref with the local one if the remote still
// holds expect ("" = absent): `lore live restore` and compaction (F8).
func (r *Ref) ForcePush(ctx context.Context, expect string) (PushOutcome, error) {
	if r.Remote == nil {
		return PushOutcome{Status: PushNoRemote}, nil
	}
	s, err := r.Snapshot(ctx)
	if err != nil {
		return PushOutcome{}, err
	}
	res, err := r.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{
		{Src: s.Local, Dst: r.Name, Mode: gitremote.ModeLease, Expect: expect},
	}})
	out := PushOutcome{Attempts: 1}
	if err != nil {
		if errors.Is(err, gitremote.ErrUnconfirmed) {
			out.Status = PushOffline
			return out, nil
		}
		return out, err
	}
	rr, _ := res.Ref(r.Name)
	switch {
	case rr.Status.Applied():
		if err := r.setTracking(ctx, s.Local); err != nil {
			return out, err
		}
		out.Status = PushDone
		return out, r.confirmed(ctx, s.Local)
	case rr.Status == gitremote.StatusStale || rr.Status == gitremote.StatusNonFastForward:
		out.Status = PushRetriesOut
	default:
		out.Status = PushRefused
	}
	out.Detail = rr.Message
	return out, nil
}

// transportStderr is git's stderr of a failed remote call ("" when err
// carries none): what PushOffline's Detail holds.
func transportStderr(err error) string {
	var te *gitremote.TransportError
	if errors.As(err, &te) {
		return te.Stderr
	}
	return ""
}
