package git

// compactv2.go — compaction with an archive (store growth item 6, devco
// D-1003, STORE_GROWTH_SPEC 9):
//
//	Compact  sync ─► the compactor must be an admin by its own replay
//	         ─► cut: the newest first-parent commit older than the cutoff
//	            whose later history (and every open layer base) descends
//	            from it (closedCut), so every kept merge keeps both parents
//	         ─► push the old tip to refs/lore-archive/epoch-<n> (or the
//	            store's archive_url); refused = nothing rewritten
//	         ─► snapshot = the cut's TRUSTED tree (a held change replaced by
//	            its last accepted version, or left out), parentless;
//	            every later commit re-made with its tree (held-at-cut
//	            files still at their held version read as the trusted one),
//	            people, dates, message and parents
//	         ─► one epoch commit (epoch n+1) whose message carries the
//	            checkpoint (Lore-Checkpoint: old tip, cut, archive, held)
//	         ─► open layers re-made on the new ids ─► force-push with lease
//	adopt    a clone whose remote has a higher epoch re-does the rewrite
//	         itself from its own history (fetching the archive when it lacks
//	         the old tip) and adopts only when every commit id comes out the
//	         same and the rewriter was an admin; anything else is refused
//	         and the clone stays on its epoch (verifyEpoch)
//	Undo     the archived tip back, the writes since the compaction re-made
//	         on it, epoch n+2 (epochs only go up), verified the same way
//
// A commit id is a hash of its tree, parents, people, dates and message,
// so the clone's own rewrite reproducing the remote's ids proves the
// snapshot is the trusted view of its own old history and every kept
// commit is unchanged: no field-by-field comparison can miss a part.

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// Compaction v2 names (file and ref formats: never renamed).
const (
	// ArchiveRefPrefix + <epoch> holds the tip of that epoch's history
	// once a compaction or an undo replaced it. It sits outside refs/lore/
	// because the org link code fetches all of refs/lore/* (a v0.1.17
	// refspec): no fetch refspec names it, so clones never download it
	// unless they verify.
	ArchiveRefPrefix = "refs/lore-archive/epoch-"
	// TrailerCheckpoint carries the checkpoint (JSON, one line) on the
	// commit that raises the epoch.
	TrailerCheckpoint = "Lore-Checkpoint"
	// TrailerSnapshot marks a compaction's parentless snapshot commit: the
	// roster replay trusts it as the root of the history (every row read
	// with the writer it states), as it trusts any root commit.
	TrailerSnapshot = "Lore-Snapshot"
	// EnvCompactV2 must be 1 for `lore store compact` to run while the
	// archive compaction is behind its flag (with compact_archive=on).
	EnvCompactV2 = "LORE_COMPACT_V2"
	// CheckpointCompact and CheckpointUndo are Checkpoint.Kind values.
	CheckpointCompact = "compact"
	CheckpointUndo    = "undo"
)

// checkpointVersion is the Checkpoint format this binary writes and reads.
const checkpointVersion = 1

// maxEpochChain bounds how many compactions in a row one adoption verifies.
const maxEpochChain = 8

// Checkpoint is what the epoch commit of a compaction or an undo records:
// enough for any clone to re-do the rewrite from its own history and for
// `lore store compact --verify` to find the archive.
type Checkpoint struct {
	V    int    `json:"v"`
	Kind string `json:"kind"`
	// FromEpoch is the epoch whose history OldTip ends; ToEpoch the new one.
	FromEpoch int `json:"from_epoch"`
	ToEpoch   int `json:"to_epoch"`
	// OldTip is the replaced history's tip, kept at Archive.
	OldTip string `json:"old_tip"`
	// Archive is the ref holding OldTip; ArchiveURL the repository it is
	// in when that is not the store's own remote.
	Archive    string `json:"archive"`
	ArchiveURL string `json:"archive_url,omitempty"`
	// Cut is the old commit the snapshot stands for (compact), CutTree its
	// raw tree, Cutoff the date asked for.
	Cut     string `json:"cut,omitempty"`
	CutTree string `json:"cut_tree,omitempty"`
	Cutoff  string `json:"cutoff,omitempty"`
	// Held lists the files held back at the cut with the version the
	// snapshot carries instead (informational: a verifier recomputes it).
	Held []CheckpointHeld `json:"held,omitempty"`
	// Restore is the archived tip an undo put back.
	Restore string `json:"restore,omitempty"`
}

// CheckpointHeld is one held file at the cut.
type CheckpointHeld struct {
	Path    string `json:"path"`
	Raw     string `json:"raw,omitempty"`
	Trusted string `json:"trusted,omitempty"`
}

// heldBlob is a held file's raw version at the cut and the trusted one
// lore reads instead ("" = absent).
type heldBlob struct{ raw, trusted string }

// CompactV2Enabled reports the hidden switch of the archive compaction
// (LORE_COMPACT_V2=1): with the store's compact_archive=on it lets `lore
// store compact` and compact=on run while the feature is behind its flag.
func CompactV2Enabled() bool { return strings.TrimSpace(os.Getenv(EnvCompactV2)) == "1" }

// encodeCheckpoint is the trailer line of cp.
func encodeCheckpoint(cp Checkpoint) (string, error) {
	raw, err := json.Marshal(cp)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: encode the compaction checkpoint", err)
	}
	return TrailerCheckpoint + ": " + string(raw), nil
}

// parseCheckpoint reads the checkpoint trailer of a commit message.
func parseCheckpoint(msg string) (Checkpoint, bool) {
	v := trailerValue(msg, TrailerCheckpoint)
	if v == "" {
		return Checkpoint{}, false
	}
	var cp Checkpoint
	if json.Unmarshal([]byte(v), &cp) != nil || cp.V != checkpointVersion || cp.OldTip == "" || cp.Archive == "" {
		return Checkpoint{}, false
	}
	if cp.Kind != CheckpointCompact && cp.Kind != CheckpointUndo {
		return Checkpoint{}, false
	}
	return cp, true
}

// archiveRef is the archive ref of an epoch.
func archiveRef(epoch int) string { return ArchiveRefPrefix + strconv.Itoa(epoch) }

// archiveRemote is where the archive lives: the archive_url the marker
// names, else the store's own remote.
func archiveRemote(url string) string {
	if strings.TrimSpace(url) != "" {
		return strings.TrimSpace(url)
	}
	return RemoteName
}

// commitMeta is a commit's people, dates (raw) and message as commit-tree
// takes them.
type commitMeta struct {
	env []string
	msg string
}

// metaOf reads commit id's people, dates and message.
func (b *Backend) metaOf(ctx context.Context, id string) (commitMeta, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "-1", "--date=raw", "--format=%an%x00%ae%x00%ad%x00%cn%x00%ce%x00%cd%x00%B", id}})
	if err != nil {
		return commitMeta{}, errs.Wrap(errs.Internal, "git: read a commit", err)
	}
	f := strings.SplitN(string(out), "\x00", commitMetaFields)
	if len(f) != commitMetaFields {
		return commitMeta{}, errs.New(errs.Internal, "git: a commit did not read back")
	}
	return commitMeta{env: []string{
		envAuthorName + "=" + f[0], envAuthorEmail + "=" + f[1], "GIT_AUTHOR_DATE=" + f[2],
		envCommitterName + "=" + f[3], envCommitterEmail + "=" + f[4], "GIT_COMMITTER_DATE=" + f[5],
	}, msg: strings.TrimRight(f[6], "\n")}, nil
}

// authorEmail is the author email in a commitMeta.
func (m commitMeta) authorEmail() string {
	for _, e := range m.env {
		if v, ok := strings.CutPrefix(e, envAuthorEmail+"="); ok {
			return strings.ToLower(strings.TrimSpace(v))
		}
	}
	return ""
}

// authorName is the author name in a commitMeta.
func (m commitMeta) authorName() string {
	for _, e := range m.env {
		if v, ok := strings.CutPrefix(e, envAuthorName+"="); ok {
			return v
		}
	}
	return ""
}

// lines runs git and splits its output into lines ("" lines dropped).
func (b *Backend) lines(ctx context.Context, what string, args ...string) ([]string, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: "+what, err)
	}
	var keep []string
	for l := range strings.SplitSeq(trimmed(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			keep = append(keep, l)
		}
	}
	return keep, nil
}

// hasCommit reports whether the clone holds commit id.
func (b *Backend) hasCommit(ctx context.Context, id string) bool {
	_, err := b.git.run(ctx, call{dir: b.work, args: []string{"cat-file", "-e", id + "^{commit}"}})
	return err == nil
}

// replayAt is the roster replay at rev, never saved: a saved replay (the
// clone's own, else the one kept at the remote tip) as is or carried on
// when rev extends it on its first-parent line, else a full replay of
// rev's history.
func (b *Backend) replayAt(ctx context.Context, rev string) (*authority, error) {
	if saved := b.authorityPath(); saved != "" {
		orgHead := b.rosterHead(ctx)
		for _, p := range []string{saved, saved + authorityBaseSuffix} {
			a := b.loadAuthorityAt(p)
			if a == nil || a.orgHead != orgHead {
				continue
			}
			if a.head == rev {
				return a, nil
			}
			if commits, ok := b.replayRangeAfter(ctx, a.head, rev); ok {
				if err := b.replayOver(ctx, a, commits); err != nil {
					return nil, err
				}
				a.head = rev
				return a, nil
			}
		}
	}
	commits, err := b.replayCommits(ctx, rev)
	if err != nil {
		return nil, err
	}
	return b.replay(ctx, rev, commits)
}

// replayCovering is a replay that reached rev with rev on its head's
// first-parent line: one of the saved replays (the clone's own, or the one
// kept at the remote tip) at rev, carried on to rev, or already past it;
// else the full replay of rev. Its adminAt(rev) and its hold log up to rev
// (heldAtCut) are those of the replay at rev. Why (devco:D-1004): a clone
// with writes of its own after the compacted tip replayed the whole old
// history to verify the compaction.
func (b *Backend) replayCovering(ctx context.Context, rev string) (*authority, error) {
	saved := b.authorityPath()
	if saved != "" {
		orgHead := b.rosterHead(ctx)
		for _, p := range []string{saved, saved + authorityBaseSuffix} {
			a := b.loadAuthorityAt(p)
			if a == nil || a.orgHead != orgHead {
				continue
			}
			if a.head == rev {
				return a, nil
			}
			if commits, ok := b.replayRangeAfter(ctx, a.head, rev); ok {
				if err := b.replayOver(ctx, a, commits); err != nil {
					return nil, err
				}
				a.head = rev
				return a, nil
			}
			if _, ok := b.afterOnFirstParentLine(ctx, rev, a.head); ok {
				return a, nil
			}
		}
	}
	commits, err := b.replayCommits(ctx, rev)
	if err != nil {
		return nil, err
	}
	return b.replay(ctx, rev, commits)
}

// adminVerdict is "" when email may rewrite the store at rev by the replay
// a (an admin, or a store whose roster names no admin: a personal store),
// else why not.
func adminVerdict(a *authority, rev, email, who string) string {
	if a == nil {
		return ""
	}
	admin, decided := a.adminAt(rev, email)
	if decided && !admin {
		return who + " is not an admin of the store's org"
	}
	return ""
}

// heldAt is the files held back at commit by the replay a there, with
// their raw version in commit and the trusted one.
func (b *Backend) heldAt(ctx context.Context, a *authority, commit string) (map[string]heldBlob, error) {
	if a == nil || len(a.trusted) == 0 {
		return nil, nil
	}
	paths := slices.Sorted(func(yield func(string) bool) {
		for p := range a.trusted {
			if !yield(p) {
				return
			}
		}
	})
	raw, err := b.blobsAt(ctx, []string{commit}, paths)
	if err != nil {
		return nil, err
	}
	out := make(map[string]heldBlob, len(paths))
	for _, p := range paths {
		out[p] = heldBlob{raw: raw[commit][p], trusted: a.trusted[p]}
	}
	return out, nil
}

// heldAtCut is heldAt(cut) read from a, the replay at a commit whose
// first-parent line holds cut: a's hold log without the commits after
// cut. Why (devco:D-1004): the replay at the cut was a second full replay
// of the history on the compacting clone and again on every adopting
// clone (9 s at 10k commits, minutes at 1M), though the replay each
// already holds at its tip saw the cut on the way. A full replay of cut
// is the fallback when cut is not on that line.
//
// Held org files (lore-org.yaml changes the org file check holds back)
// are among them: a snapshot is the trusted view of the org file too.
func (b *Backend) heldAtCut(ctx context.Context, a *authority, cut string) (map[string]heldBlob, error) {
	if a == nil {
		return nil, nil
	}
	var skip map[string]bool
	trusted := maps.Clone(a.trusted)
	if a.head != cut {
		after, ok := b.afterOnFirstParentLine(ctx, cut, a.head)
		if ok {
			skip, trusted = after, holdsWithout(a.holds, after)
		} else {
			at, err := b.replayAt(ctx, cut)
			if err != nil {
				return nil, err
			}
			a, trusted = at, maps.Clone(at.trusted)
		}
	}
	if trusted == nil {
		trusted = map[string]string{}
	}
	maps.Copy(trusted, orgFileHolds(a, skip))
	return b.heldAt(ctx, &authority{trusted: trusted}, cut)
}

// orgFileHolds is the org files (lore-org.yaml) held back after a's
// replay without the commits in skip, by the rule of the org file check
// (adapters/orgcfg acceptedOrgFileIndexed): a version is accepted when it
// is the history's first one at a root commit, puts back the accepted
// version, or its author is an admin at its parent (or the roster decides
// nothing there); a removal is not judged. It returns path ─▶ the last
// accepted version ("" = none) for each file whose newest version is
// held. Why (devco:D-1004): the snapshot replaced held row files only, so
// a member's held edit of the org file became the snapshot's version, and
// a fresh clone trusts the snapshot root.
func orgFileHolds(a *authority, skip map[string]bool) map[string]string {
	accepted := map[string]string{}
	seen := map[string]bool{}
	held := map[string]string{}
	for _, c := range a.orgFile {
		if skip[c.commit] || zeroBlob(c.blob) {
			continue
		}
		admin, decided := false, false
		if c.parent != "" {
			admin, decided = a.adminAt(c.parent, c.author)
		}
		ok := (seen[c.path] && c.blob == accepted[c.path]) || (!seen[c.path] && c.parent == "") || admin || !decided
		if !ok {
			held[c.path] = accepted[c.path]
			continue
		}
		accepted[c.path], seen[c.path] = c.blob, true
		delete(held, c.path)
	}
	return held
}

// holdsWithout folds a hold log, leaving out the commits in skip: held
// path ─▶ trusted version.
func holdsWithout(holds []holdEvent, skip map[string]bool) map[string]string {
	trusted := map[string]string{}
	for _, h := range holds {
		switch {
		case skip[h.commit]:
		case h.held:
			trusted[h.path] = h.trusted
		default:
			delete(trusted, h.path)
		}
	}
	return trusted
}

// afterOnFirstParentLine is the set of commits of cut..head when cut is
// on head's first-parent line (ok false otherwise): then a replay of
// head decided every commit of cut's history before any of these
// (replayRangeAfter), so leaving them out leaves the replay at cut.
func (b *Backend) afterOnFirstParentLine(ctx context.Context, cut, head string) (map[string]bool, bool) {
	ls, err := b.lines(ctx, "list the commits after the cut", "rev-list", "--parents", cut+".."+head)
	if err != nil {
		return nil, false
	}
	first := make(map[string]string, len(ls))
	after := make(map[string]bool, len(ls))
	for _, l := range ls {
		f := strings.Fields(l)
		after[f[0]] = true
		if len(f) > 1 {
			first[f[0]] = f[1]
		} else {
			first[f[0]] = ""
		}
	}
	for id := head; id != cut; {
		fp, ok := first[id]
		if !ok || fp == "" {
			return nil, false
		}
		delete(first, id) // each commit once: a broken listing cannot loop
		id = fp
	}
	return after, true
}

// blobsAt is, for each commit (or tree), the blob of each path ("" when
// absent), read with one cat-file process. Only the object names are
// asked for, so git reads trees and never the blobs: a blobless clone
// (store.clone_filter) answers without fetching anything.
func (b *Backend) blobsAt(ctx context.Context, revs, paths []string) (map[string]map[string]string, error) {
	out := make(map[string]map[string]string, len(revs))
	if len(paths) == 0 {
		return out, nil
	}
	var in strings.Builder
	for _, r := range revs {
		out[r] = map[string]string{}
		for _, p := range paths {
			in.WriteString(r + ":" + p + "\n")
		}
	}
	res, err := b.git.run(ctx, call{dir: b.work, args: []string{"cat-file", "--batch-check=%(objectname)"}, stdin: []byte(in.String())})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read held files", err)
	}
	got := strings.Split(strings.TrimRight(string(res), "\n"), "\n")
	i := 0
	for _, r := range revs {
		for _, p := range paths {
			if i >= len(got) {
				return nil, errs.New(errs.Internal, "git: cat-file answered fewer lines than asked")
			}
			if f := strings.Fields(got[i]); len(f) == 1 && commitIDPattern.MatchString(f[0]) {
				out[r][p] = f[0] // "<rev>:<path> missing" when no tree names the path
			}
			i++
		}
	}
	return out, nil
}

// overlaid is tree with every held file whose version in it is still the
// raw held one (cur) set to its trusted version (removed when it never had
// one); tree itself when nothing changes.
func (b *Backend) overlaid(ctx context.Context, tree string, cur map[string]string, held map[string]heldBlob) (string, error) {
	var edits strings.Builder
	set := overlayEdits(cur, held)
	for _, p := range sortedKeys(set) {
		if set[p] == "" {
			edits.WriteString("0 " + strings.Repeat("0", len(emptyTree)) + "\t" + p + "\n")
		} else {
			edits.WriteString("100644 " + set[p] + "\t" + p + "\n")
		}
	}
	if edits.Len() == 0 {
		return tree, nil
	}
	gitDir, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: locate the clone's git dir", err)
	}
	// a private index file of its own per call: a fixed name collided with
	// another lore process verifying the same clone (its .lock)
	f, err := os.CreateTemp(trimmed(gitDir), "lore-compact-index-")
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: make a private index", err)
	}
	idx := f.Name()
	_ = f.Close()
	_ = os.Remove(idx) // git writes it from scratch (an empty file is not an index)
	defer func() { _ = os.Remove(idx); _ = os.Remove(idx + ".lock") }()
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err = b.git.run(ctx, call{dir: b.work, args: []string{"read-tree", tree}, env: env}); err == nil {
		_, err = b.git.run(ctx, call{dir: b.work, args: []string{"update-index", "--index-info"}, env: env, stdin: []byte(edits.String())})
	}
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: build the trusted tree: "+stderrOf(err), err)
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"write-tree"}, env: env})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: write the trusted tree", err)
	}
	return trimmed(out), nil
}

// overlayEdits is what overlaid changes in a tree whose held files read
// cur: path ─▶ trusted blob ("" = removed) for each held file still at
// its raw held version.
func overlayEdits(cur map[string]string, held map[string]heldBlob) map[string]string {
	var set map[string]string
	for p, h := range held {
		if cur[p] != h.raw || h.raw == h.trusted {
			continue
		}
		if set == nil {
			set = map[string]string{}
		}
		set[p] = h.trusted
	}
	return set
}

// keptCommit is one commit after the cut: id, tree, parents.
type keptCommit struct {
	id, tree string
	parents  []string
}

// keptAfter lists cut..tip parents first (reverse topological order).
func (b *Backend) keptAfter(ctx context.Context, cut, tip string) ([]keptCommit, error) {
	ls, err := b.lines(ctx, "list the history after the cut", "log", "--reverse", "--topo-order", "--format=%H %T %P", cut+".."+tip)
	if err != nil {
		return nil, err
	}
	out := make([]keptCommit, 0, len(ls))
	for _, l := range ls {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		out = append(out, keptCommit{id: f[0], tree: f[1], parents: f[2:]})
	}
	return out, nil
}

// rewriteFrom makes the snapshot of cut (its trusted tree, snap's people,
// dates and message, no parent) and re-makes every commit of cut..tip on
// it, parents mapped, tree overlaid. It returns the new tip and the old ─▶
// new id of every commit it made. A parent outside cut..tip other than cut
// itself is an error: closedCut picks cuts that have none.
func (b *Backend) rewriteFrom(ctx context.Context, cut, tip string, held map[string]heldBlob, snap commitMeta) (string, map[string]string, error) {
	kept, err := b.keptAfter(ctx, cut, tip)
	if err != nil {
		return "", nil, err
	}
	cutTree, err := b.treeOf(ctx, cut)
	if err != nil {
		return "", nil, err
	}
	paths := slices.Sorted(func(yield func(string) bool) {
		for p := range held {
			if !yield(p) {
				return
			}
		}
	})
	revs := []string{cut}
	for _, k := range kept {
		revs = append(revs, k.id)
	}
	cur, err := b.blobsAt(ctx, revs, paths)
	if err != nil {
		return "", nil, err
	}
	snapTree, err := b.overlaid(ctx, cutTree, cur[cut], held)
	if err != nil {
		return "", nil, err
	}
	s, err := b.commitTree(ctx, snapTree, nil, snap.msg, snap.env)
	if err != nil {
		return "", nil, err
	}
	idMap := map[string]string{cut: s}
	parents := make([][]string, len(kept))
	edits := make([]map[string]string, len(kept))
	for i, k := range kept {
		for _, p := range k.parents {
			np, ok := idMap[p]
			if !ok {
				return "", nil, errs.New(errs.Internal, "git: commit "+short(k.id)+" has a parent older than the compaction cut")
			}
			parents[i] = append(parents[i], np)
		}
		edits[i] = overlayEdits(cur[k.id], held)
		idMap[k.id] = markOf(i)
	}
	if err := b.writeKept(ctx, kept, parents, edits, nil, idMap); err != nil {
		return "", nil, err
	}
	return idMap[tipOr(kept, cut)], idMap, nil
}

// writeKept re-makes kept (each with its tree plus edits[i], parents[i]
// as ids or marks of earlier ones, its people, dates and message, msg
// changing the message when set) in one fastWrite, and sets idMap of
// each to its new id (callers stored its mark there).
func (b *Backend) writeKept(ctx context.Context, kept []keptCommit, parents [][]string, edits []map[string]string, msg func(string) string, idMap map[string]string) error {
	if len(kept) == 0 {
		return nil
	}
	ids := make([]string, len(kept))
	for i, k := range kept {
		ids[i] = k.id
	}
	metas, err := b.metasOf(ctx, ids)
	if err != nil {
		return err
	}
	fc := make([]fastCommit, len(kept))
	for i, k := range kept {
		m := metas[k.id]
		if msg != nil {
			m.msg = msg(m.msg)
		}
		fc[i] = fastCommit{tree: k.tree, parents: parents[i], meta: m}
		if edits != nil {
			fc[i].edits = edits[i]
		}
	}
	made, err := b.fastWrite(ctx, fc)
	if err != nil {
		return err
	}
	for i, k := range kept {
		idMap[k.id] = made[i]
	}
	return nil
}

// tipOr is the last kept commit's id, or cut when nothing is kept.
func tipOr(kept []keptCommit, cut string) string {
	if len(kept) == 0 {
		return cut
	}
	return kept[len(kept)-1].id
}

// remakeOnto re-makes from..tip (exclusive of from) with from mapped to
// onto, trees as they are: an undo's writes since the compaction.
func (b *Backend) remakeOnto(ctx context.Context, from, tip, onto string) (string, map[string]string, error) {
	kept, err := b.keptAfter(ctx, from, tip)
	if err != nil {
		return "", nil, err
	}
	idMap := map[string]string{from: onto}
	parents := make([][]string, len(kept))
	for i, k := range kept {
		for _, p := range k.parents {
			np, ok := idMap[p]
			if !ok {
				return "", nil, errs.New(errs.Internal, "git: commit "+short(k.id)+" has a parent older than the compaction")
			}
			parents[i] = append(parents[i], np)
		}
		idMap[k.id] = markOf(i)
	}
	if err := b.writeKept(ctx, kept, parents, nil, nil, idMap); err != nil {
		return "", nil, err
	}
	return idMap[tipOr(kept, from)], idMap, nil
}

// openLayerRefs is ref ─▶ tip of every layer ref of the clone, its own
// and the fetched copies, open and (with trash) trashed: each one keeps
// the commits it reaches downloadable by every clone, so a compaction
// moves them onto the new history too.
func (b *Backend) openLayerRefs(ctx context.Context, trash bool) (map[string]string, error) {
	prefixes := []string{branchlayer.RefPrefix, LayerTrackingPrefix}
	if trash {
		prefixes = append(prefixes, branchlayer.TrashPrefix, LayerTrashTrackingPrefix)
	}
	refs, err := b.lines(ctx, "list the layers", append([]string{"for-each-ref", "--format=%(refname) %(objectname)"}, prefixes...)...)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, l := range refs {
		ref, tip, ok := strings.Cut(l, " ")
		if ok {
			out[ref] = tip
		}
	}
	return out, nil
}

// layerBase is the store commit a layer tip names as its base, else the
// newest commit of h's history it starts from ("" when it shares none).
func (b *Backend) layerBase(ctx context.Context, tip, h string) string {
	msg, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "-1", "--format=%B", tip}})
	if err != nil {
		return ""
	}
	if base := trailerValue(string(msg), TrailerStoreBase); base != "" {
		return base
	}
	if mb, err := b.lines(ctx, "find a layer's base", "merge-base", tip, h); err == nil && len(mb) == 1 {
		return mb[0]
	}
	return ""
}

// closedCut moves cut back (older) along chain until every commit of
// chain[cut]..h has its parents in that range or at the cut, and every
// open layer based on h's history is based in it: then every kept merge
// keeps both parents and every open layer can be re-made on the new ids.
// -1 when only the root is left.
func (b *Backend) closedCut(ctx context.Context, chain []chainCommit, cut int, h string) (int, error) {
	layers, err := b.openLayerRefs(ctx, false)
	if err != nil {
		return -1, err
	}
	var bases []string
	for _, tip := range layers {
		if base := b.layerBase(ctx, tip, h); base != "" {
			if anc, _ := b.isAncestor(ctx, base, h); anc {
				bases = append(bases, base)
			}
		}
	}
	pos := make(map[string]int, len(chain))
	for i, c := range chain {
		pos[c.id] = i
	}
	for cut >= 0 && cut < len(chain)-1 {
		c := chain[cut].id
		kept, err := b.keptAfter(ctx, c, h)
		if err != nil {
			return -1, err
		}
		in := map[string]bool{c: true}
		for _, k := range kept {
			in[k.id] = true
		}
		bad := ""
		for _, k := range kept {
			if i := slices.IndexFunc(k.parents, func(p string) bool { return !in[p] }); i >= 0 {
				bad = k.parents[i]
				break
			}
		}
		if bad == "" {
			if i := slices.IndexFunc(bases, func(p string) bool { return !in[p] }); i >= 0 {
				bad = bases[i]
			}
		}
		if bad == "" {
			return cut, nil
		}
		next := cut + 1
		if fork, ferr := b.lines(ctx, "find a fork point", "merge-base", c, bad); ferr == nil && len(fork) == 1 {
			if i, ok := pos[fork[0]]; ok && i > cut {
				next = i
			}
		}
		cut = next
	}
	return -1, nil
}

// rebaseLayers moves every layer ref (refs, own or fetched, open or
// trashed) onto the new ids: a tip the rewrite re-made (a landed layer's
// commits are in the store branch's history) points at its new commit;
// a layer whose base idMap maps is re-made on the new base (same trees,
// the base trailer naming the new base). h is the old tip. It returns
// ref ─▶ new tip for the refs it moved; a ref it cannot move (a trashed
// layer older than the cut) stays where it was.
func (b *Backend) rebaseLayers(ctx context.Context, refs map[string]string, idMap map[string]string, h string) (map[string]string, error) {
	moved := map[string]string{}
	for _, ref := range slices.Sorted(func(yield func(string) bool) {
		for r := range refs {
			if !yield(r) {
				return
			}
		}
	}) {
		tip := refs[ref]
		if n, ok := idMap[tip]; ok {
			if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", ref, n, tip}}); err != nil {
				return nil, errs.Wrap(errs.Internal, "git: move the layer "+ref+" onto the compacted history", err)
			}
			moved[ref] = n
			continue
		}
		base := b.layerBase(ctx, tip, h)
		nb, ok := idMap[base]
		if base == "" || !ok {
			continue
		}
		commits, err := b.keptAfter(ctx, base, tip)
		if err != nil {
			return nil, err
		}
		m := map[string]string{base: nb}
		var made []keptCommit
		var parents [][]string
		for _, c := range commits {
			var ps []string
			for _, p := range c.parents {
				np, ok := m[p]
				if !ok {
					np, ok = idMap[p]
				}
				if !ok {
					ps = nil
					break
				}
				ps = append(ps, np)
			}
			if ps == nil {
				break // a parent outside the layer and the rewrite: the rest stays
			}
			m[c.id] = markOf(len(made))
			made = append(made, c)
			parents = append(parents, ps)
		}
		rebase := func(msg string) string {
			return strings.ReplaceAll(msg, TrailerStoreBase+": "+base, TrailerStoreBase+": "+nb)
		}
		if err := b.writeKept(ctx, made, parents, nil, rebase, m); err != nil {
			return nil, err
		}
		if n, ok := m[tip]; ok {
			if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", ref, n, tip}}); err != nil {
				return nil, errs.Wrap(errs.Internal, "git: move the layer "+ref+" onto the compacted history", err)
			}
			moved[ref] = n
		}
	}
	return moved, nil
}

// pushArchive puts tip at the archive ref without force: an archive is
// never overwritten. Anything but success changes nothing.
func (b *Backend) pushArchive(ctx context.Context, url, ref, tip string) error {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"push", "--porcelain", "--no-verify", "--", archiveRemote(url), tip + ":" + ref}})
	if err != nil {
		if _, ran := exitCode(err); !ran {
			return err
		}
	}
	flag, summary, found := porcelainVerdict(string(out), ref)
	switch {
	case !found:
		return errs.New(errs.Offline, "the archive "+ref+" could not be pushed (unreachable); nothing was rewritten").
			WithHint("run the compaction again when the store is reachable")
	case flag == flagNew || flag == flagUpToDate || flag == flagFastForward:
		return nil
	default:
		return errs.New(errs.Refused, "the archive "+ref+" was refused ("+summary+"); nothing was rewritten").
			WithHint("the account needs push access to " + ref + " (or set archive_url to a repository it may push to)")
	}
}

// fetchArchive brings ref from the archive into the clone (same ref name).
func (b *Backend) fetchArchive(ctx context.Context, url, ref string) error {
	if NoFetch(ctx) {
		return errs.New(errs.Offline, "the archive "+ref+" is not in this clone and this run does not fetch")
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--", archiveRemote(url), "+" + ref + ":" + ref}}); err != nil {
		return errs.Wrap(errs.Offline, "git: fetch the archive "+ref, err)
	}
	return nil
}

// haveOrFetch makes sure the clone holds commit id, fetching the archive
// ref of cp when it does not.
func (b *Backend) haveOrFetch(ctx context.Context, id string, cp Checkpoint) bool {
	if b.hasCommit(ctx, id) {
		return true
	}
	if b.fetchArchive(ctx, cp.ArchiveURL, cp.Archive) != nil {
		return false
	}
	return b.hasCommit(ctx, id)
}

// findCheckpoint is the newest commit on tip's first-parent line whose
// message carries a checkpoint, with it ("" when none).
func (b *Backend) findCheckpoint(ctx context.Context, tip string) (string, Checkpoint, error) {
	ids, err := b.lines(ctx, "find the compaction checkpoint", "log", "--first-parent", "--format=%H", "--grep=^"+TrailerCheckpoint+": ", tip)
	if err != nil {
		return "", Checkpoint{}, err
	}
	for _, id := range ids {
		m, err := b.metaOf(ctx, id)
		if err != nil {
			return "", Checkpoint{}, err
		}
		if cp, ok := parseCheckpoint(m.msg); ok {
			return id, cp, nil
		}
	}
	return "", Checkpoint{}, nil
}

// epochProof is what a verified epoch adoption knows.
type epochProof struct {
	oldTip string            // the replaced history's tip (this clone's line)
	idMap  map[string]string // old ─▶ new id of every re-made commit
}

// epochRefusal is why an epoch does not verify, in a few words.
func epochRefusal(epoch int, who, why string) string {
	if who == "" {
		who = "someone"
	}
	return "rewritten to epoch " + strconv.Itoa(epoch) + " by " + who + " and this clone cannot verify it (" + why + ")"
}

// adoptRefusal is the one line a sync prints for a refused epoch; store is
// the store's url (every store command takes it), so the printed command
// runs as printed.
func adoptRefusal(store, why string) string {
	return "the store's history was " + why + "; it stays on its own history: check it with `lore store compact --store " + store + " --verify` and ask an admin"
}

// verifyEpoch re-does the rewrite that made remote's epoch from this
// clone's own history (h) and reports why it is refused ("" = verified).
//
// own is the clone's own HEAD: the old history must share it. "" (lore
// store compact --verify) checks the archived history itself instead,
// back through every earlier compaction.
func (b *Backend) verifyEpoch(ctx context.Context, remote, own string, depth int) (epochProof, string, error) {
	re := b.epochAt(ctx, remote)
	e, cp, err := b.findCheckpoint(ctx, remote)
	if err != nil {
		return epochProof{}, "", err
	}
	if e == "" || cp.ToEpoch != re {
		who := ""
		if m, merr := b.metaOf(ctx, remote); merr == nil {
			who = m.authorName() + " <" + m.authorEmail() + ">"
		}
		return epochProof{}, epochRefusal(re, who, "no compaction checkpoint names this epoch"), nil
	}
	em, err := b.metaOf(ctx, e)
	if err != nil {
		return epochProof{}, "", err
	}
	who := em.authorName() + " <" + em.authorEmail() + ">"
	refuse := func(why string) (epochProof, string, error) {
		return epochProof{}, epochRefusal(re, who, why), nil
	}
	parents, err := b.lines(ctx, "read the epoch commit", "rev-list", "--parents", "-n", "1", e)
	if err != nil {
		return epochProof{}, "", err
	}
	f := strings.Fields(strings.Join(parents, " "))
	if len(f) != 2 {
		return refuse("the epoch commit does not have exactly one parent")
	}
	p := f[1]
	if why := b.epochCommitOnlyRaises(ctx, e, p, re); why != "" {
		return refuse(why)
	}
	if !b.haveOrFetch(ctx, cp.OldTip, cp) {
		return refuse("the old history " + short(cp.OldTip) + " is not in this clone and its archive " + cp.Archive + " could not be fetched")
	}
	oe := b.epochAt(ctx, cp.OldTip)
	var inner *epochProof
	switch le := b.epochAt(ctx, own); {
	case oe > le || (own == "" && oe > 0):
		if depth >= maxEpochChain {
			return refuse("too many compactions in a row to verify")
		}
		pr, why, err := b.verifyEpoch(ctx, cp.OldTip, own, depth+1)
		if err != nil || why != "" {
			return epochProof{}, why, err
		}
		inner = &pr
	case own == "":
	case oe == le:
		if mb, _ := b.lines(ctx, "compare histories", "merge-base", own, cp.OldTip); len(mb) == 0 {
			return refuse("the old history it names is not this clone's")
		}
	default:
		return refuse("the old history it names is older than this clone's epoch")
	}
	old, err := b.replayCovering(ctx, cp.OldTip)
	if err != nil {
		return epochProof{}, "", err
	}
	if why := adminVerdict(old, cp.OldTip, em.authorEmail(), who); why != "" {
		return refuse(why)
	}
	var proof epochProof
	var why string
	if cp.Kind == CheckpointCompact {
		proof, why, err = b.verifyCompact(ctx, cp, p, old, refuse)
	} else {
		proof, why, err = b.verifyUndo(ctx, cp, p, refuse)
	}
	if err != nil || why != "" || inner == nil {
		return proof, why, err
	}
	// an older compaction verified first: the clone's own line is its old
	// tip, and its ids map on through this one's
	for k, v := range inner.idMap {
		if n, ok := proof.idMap[v]; ok {
			inner.idMap[k] = n
		}
	}
	maps.Copy(proof.idMap, inner.idMap)
	proof.oldTip = inner.oldTip
	return proof, "", nil
}

// verifyCompact checks a compaction: the clone's own rewrite of cp.OldTip
// at cp.Cut, with the snapshot's people and message, must be p. old is
// the clone's replay at cp.OldTip: the files held at the cut come from it.
func (b *Backend) verifyCompact(ctx context.Context, cp Checkpoint, p string, old *authority, refuse func(string) (epochProof, string, error)) (epochProof, string, error) {
	if anc, _ := b.isAncestor(ctx, cp.Cut, cp.OldTip); !anc {
		return refuse("its cut is not in the old history")
	}
	roots, err := b.lines(ctx, "find the snapshot", "rev-list", "--max-parents=0", p)
	if err != nil {
		return epochProof{}, "", err
	}
	if len(roots) != 1 {
		return refuse("the new history does not start at one snapshot")
	}
	snap, err := b.metaOf(ctx, roots[0])
	if err != nil {
		return epochProof{}, "", err
	}
	held, err := b.heldAtCut(ctx, old, cp.Cut)
	if err != nil {
		return epochProof{}, "", err
	}
	tip, idMap, err := b.rewriteFrom(ctx, cp.Cut, cp.OldTip, held, snap)
	if err != nil {
		return refuse(err.Error())
	}
	if tip != p {
		if idMap[cp.Cut] != roots[0] {
			return refuse("its snapshot is not the trusted view of the old history at the cut")
		}
		return refuse("its kept commits differ from the old history")
	}
	return epochProof{oldTip: cp.OldTip, idMap: idMap}, "", nil
}

// verifyUndo checks an undo: cp.Restore must be the tip the compaction
// that made cp.OldTip's epoch archived, and the writes after that
// compaction re-made on it must be p.
func (b *Backend) verifyUndo(ctx context.Context, cp Checkpoint, p string, refuse func(string) (epochProof, string, error)) (epochProof, string, error) {
	prevE, prev, err := b.findCheckpoint(ctx, cp.OldTip)
	if err != nil {
		return epochProof{}, "", err
	}
	if prevE == "" || prev.OldTip != cp.Restore {
		return refuse("the history it restores is not the one the last compaction archived")
	}
	if !b.haveOrFetch(ctx, cp.Restore, prev) {
		return refuse("the restored history " + short(cp.Restore) + " could not be fetched from " + prev.Archive)
	}
	tip, idMap, err := b.remakeOnto(ctx, prevE, cp.OldTip, cp.Restore)
	if err != nil {
		return refuse(err.Error())
	}
	if tip != p {
		return refuse("the writes since the compaction differ from this clone's")
	}
	return epochProof{oldTip: cp.OldTip, idMap: idMap}, "", nil
}

// epochCommitOnlyRaises is "" when commit e (parent p) changes only
// lore-store.yaml, and only its epoch (to epoch).
func (b *Backend) epochCommitOnlyRaises(ctx context.Context, e, p string, epoch int) string {
	et, err := b.treeOf(ctx, e)
	if err != nil {
		return "its epoch commit cannot be read"
	}
	pt, err := b.treeOf(ctx, p)
	if err != nil {
		return "its epoch commit cannot be read"
	}
	back, err := b.withFile(ctx, et, rowfs.MarkerFile, b.blobAt(ctx, p, rowfs.MarkerFile))
	if err != nil || back != pt {
		return "its epoch commit changes more than the store epoch"
	}
	em, err := b.markerAt(ctx, e)
	if err != nil {
		return "its epoch commit has no readable " + rowfs.MarkerFile
	}
	pm, err := b.markerAt(ctx, p)
	if err != nil {
		return "the history before its epoch commit has no readable " + rowfs.MarkerFile
	}
	if em.Epoch != epoch {
		return "its epoch commit does not raise the epoch to " + strconv.Itoa(epoch)
	}
	em.Epoch, pm.Epoch = 0, 0
	ea, _ := em.Encode()
	pa, _ := pm.Encode()
	if string(ea) != string(pa) {
		return "its epoch commit changes more of " + rowfs.MarkerFile + " than the epoch"
	}
	return ""
}

// epochCommitV2 adds the commit raising the epoch to m.Epoch, its message
// carrying cp.
func (b *Backend) epochCommitV2(ctx context.Context, tip string, m rowfs.Marker, cp Checkpoint) (string, error) {
	raw, err := m.Encode()
	if err != nil {
		return "", err
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"hash-object", "-w", "--stdin"}, stdin: raw})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: write the marker", err)
	}
	t, err := b.treeOf(ctx, tip)
	if err != nil {
		return "", err
	}
	tree, err := b.withFile(ctx, t, rowfs.MarkerFile, trimmed(out))
	if err != nil {
		return "", err
	}
	line, err := encodeCheckpoint(cp)
	if err != nil {
		return "", err
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return "", err
	}
	subject := compactSubject + strconv.Itoa(m.Epoch) + ")"
	if cp.Kind == CheckpointUndo {
		subject = undoSubject + strconv.Itoa(cp.FromEpoch-1) + ", epoch " + strconv.Itoa(m.Epoch) + ")"
	}
	return b.commitTree(ctx, tree, []string{tip}, b.cfg.withTrailers(ctx, subject, line), env)
}

// undoSubject starts an undo's epoch commit.
const undoSubject = "lore: undo the compaction of the store history (back to epoch "

// compactorCheck refuses a compactor the replay at h does not hold for an
// admin, and returns the compactor's commit people.
func (b *Backend) compactorCheck(ctx context.Context, h string) ([]string, *authority, error) {
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return nil, nil, err
	}
	// the author git will write (the env above, else the clone's git user)
	ident, err := b.git.run(ctx, call{dir: b.work, args: []string{"var", "GIT_AUTHOR_IDENT"}, env: env})
	if err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "git: read the compactor's identity", err)
	}
	_, rest, _ := strings.Cut(string(ident), "<")
	email, _, _ := strings.Cut(rest, ">")
	email = strings.ToLower(strings.TrimSpace(email))
	a, err := b.replayAt(ctx, h)
	if err != nil {
		return nil, nil, err
	}
	if why := adminVerdict(a, h, email, email); why != "" {
		return nil, nil, errs.New(errs.Refused, "store compact: "+why+" by the store's own roster; only an admin rewrites the store history").
			WithHint("ask an org admin to run it")
	}
	return env, a, nil
}

// snapshotMeta is the snapshot commit's people, date (the cut's) and
// message.
func (b *Backend) snapshotMeta(ctx context.Context, env []string, cut chainCommit, squashed int, cutoff time.Time, epoch int) commitMeta {
	date := "@" + strconv.FormatInt(cut.at.Unix(), 10) + " +0000"
	env = append(slices.Clone(env), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	msg := snapshotSubject + cutoff.UTC().Format(time.DateOnly) + "\n\n" + strconv.Itoa(squashed) + " commits squashed; every row is kept (held changes as their last accepted version)\n\n" +
		TrailerSnapshot + ": epoch-" + strconv.Itoa(epoch)
	return commitMeta{env: env, msg: msg}
}

// loreTrackingNS is where a clone keeps its fetched copy of the remote's
// refs/lore/* refs (the org link code's refspec; layers, live, signals).
const loreTrackingNS = "refs/lore-remote/" + RemoteName + "/"

// remoteRefOf is the remote ref a lore ref of the clone stands for: its
// own name, or for a fetched copy the name on the remote.
func remoteRefOf(ref string) string {
	if rest, ok := strings.CutPrefix(ref, loreTrackingNS); ok {
		return "refs/lore/" + rest
	}
	return ref
}

// trackingOfLore is the fetched copy of a remote refs/lore/* ref.
func trackingOfLore(dst string) string { return loreTrackingNS + strings.TrimPrefix(dst, "refs/lore/") }

// movedRefPrefixes are the lore refs besides layers that can reach the
// store branch's history (claims on the signals ref, v0.1.17 live rows),
// own and fetched: a compaction moves them so no ref keeps a pre-cut
// commit downloadable (coordinator decision for devco D-1003).
var movedRefPrefixes = []string{"refs/lore/signals/", "refs/lore/live/", loreTrackingNS + "signals/", loreTrackingNS + "live/"}

// otherLoreRefs is ref ─▶ tip of every movedRefPrefixes ref of the clone.
func (b *Backend) otherLoreRefs(ctx context.Context) (map[string]string, error) {
	refs, err := b.lines(ctx, "list the lore refs", append([]string{"for-each-ref", "--format=%(refname) %(objectname)"}, movedRefPrefixes...)...)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, l := range refs {
		if ref, tip, ok := strings.Cut(l, " "); ok {
			out[ref] = tip
		}
	}
	return out, nil
}

// moveOffHistory is tip moved off h's replaced history: a commit the
// rewrite re-made is its new id; a commit of h's history the rewrite
// squashed is snap (the snapshot holds its rows); a chain of its own on
// top of h's history is re-made with every parent mapped, a squashed
// parent dropped. moved is false when tip does not reach h's history
// (nothing to move) or cannot be placed (snap "" on an undo).
func (b *Backend) moveOffHistory(ctx context.Context, tip, h, snap string, idMap map[string]string) (string, bool, error) {
	if n, ok := idMap[tip]; ok {
		return n, n != tip, nil
	}
	if anc, err := b.isAncestor(ctx, tip, h); err != nil {
		return "", false, err
	} else if anc {
		return snap, snap != "", nil
	}
	if mb, _ := b.lines(ctx, "compare a lore ref", "merge-base", tip, h); len(mb) == 0 {
		return "", false, nil // its own history: nothing of h's in it
	}
	ls, err := b.lines(ctx, "list a lore ref", "log", "--reverse", "--topo-order", "--format=%H %T %P", tip, "--not", h)
	if err != nil {
		return "", false, err
	}
	m := map[string]string{}
	var made []keptCommit
	var parents [][]string
	for _, l := range ls {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		var ps []string
		for _, p := range f[2:] {
			if np, ok := m[p]; ok {
				ps = append(ps, np)
			} else if np, ok := idMap[p]; ok {
				ps = append(ps, np)
			} else if snap != "" && !slices.Contains(ps, snap) {
				ps = append(ps, snap) // a squashed parent: the snapshot stands for it
			}
		}
		m[f[0]] = markOf(len(made))
		made = append(made, keptCommit{id: f[0], tree: f[1]})
		parents = append(parents, ps)
	}
	if err := b.writeKept(ctx, made, parents, nil, nil, m); err != nil {
		return "", false, err
	}
	n, ok := m[tip]
	return n, ok, nil
}

// archivedRefPrefix + <epoch>/<name> keeps a layer ref the compaction of
// that epoch could not move (a trashed layer older than the cut).
const archivedRefPrefix = "refs/lore-archive/refs-epoch-"

// pushMovedRefs force-pushes every moved lore ref with a lease on the
// remote copy it was made from, and moves each stuck one (a layer ref
// that reaches the old history and cannot move) into the archive
// namespace, deleting it from refs/lore/: one atomic push, so no ref the
// remote keeps under refs/lore/ reaches the replaced history.
func (b *Backend) pushMovedRefs(ctx context.Context, moved, stuck, was map[string]string, epoch int) error {
	// one update per remote ref: the clone's own ref wins over its fetched copy
	byDst := map[string]string{}
	for ref, tip := range moved {
		dst := remoteRefOf(ref)
		if _, own := byDst[dst]; !own || dst == ref {
			byDst[dst] = tip
		}
	}
	gone := map[string]string{}
	for ref, tip := range stuck {
		gone[remoteRefOf(ref)] = tip
	}
	var args, refspecs []string
	for _, dst := range slices.Sorted(maps.Keys(byDst)) {
		lease := was[trackingOfLore(dst)]
		if lease == "" {
			continue // a ref the remote does not hold: its own sync pushes it
		}
		args = append(args, "--force-with-lease="+dst+":"+lease)
		refspecs = append(refspecs, byDst[dst]+":"+dst)
	}
	for _, dst := range slices.Sorted(maps.Keys(gone)) {
		refspecs = append(refspecs, gone[dst]+":"+archivedRefPrefix+strconv.Itoa(epoch)+"/"+strings.TrimPrefix(dst, "refs/lore/"))
		if lease := was[trackingOfLore(dst)]; lease != "" {
			args = append(args, "--force-with-lease="+dst+":"+lease)
			refspecs = append(refspecs, ":"+dst)
		}
	}
	if len(refspecs) > 0 {
		all := append(append([]string{"push", "--porcelain", "--no-verify", "--atomic"}, args...), "--", RemoteName)
		if _, err := b.git.run(ctx, call{dir: b.work, args: append(all, refspecs...)}); err != nil {
			return errs.Wrap(errs.Contended, "git: push the lore refs moved onto the compacted history", err)
		}
	}
	for dst, tip := range byDst {
		if was[trackingOfLore(dst)] != "" {
			_, _ = b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", trackingOfLore(dst), tip}})
		}
	}
	for ref := range stuck {
		_, _ = b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", "-d", ref}})
	}
	return nil
}

// refusalOf is a verified-adoption refusal as a sync reports it.
func refusalOf(reason string) []store.Refusal {
	return []store.Refusal{{Code: errs.Refused, Reason: reason}}
}

// UndoRequest asks to put back the history a compaction replaced.
type UndoRequest struct {
	// Epoch is the epoch whose archived history comes back: the newest
	// compaction must have ended it (`--undo-epoch <n>`).
	Epoch  int
	DryRun bool
	Squash bool
}

// UndoEpoch puts back the archived tip of the newest compaction, the
// writes made since re-made on it, as a new epoch (epochs only go up, so
// every clone adopts it through the same verified path). The compacted
// history is archived first, so an undo is itself undoable.
func (b *Backend) UndoEpoch(ctx context.Context, req UndoRequest) (CompactResult, error) {
	if err := b.usable(ctx); err != nil {
		return CompactResult{}, err
	}
	if b.mode == modeRepo {
		return CompactResult{}, errs.New(errs.Refused, "git: lore never rewrites a code repository's history")
	}
	if err := b.refusePersonGitOp(ctx, b.work); err != nil {
		return CompactResult{}, err
	}
	if !req.DryRun {
		if err := b.compactSync(ctx, req.Squash); err != nil {
			return CompactResult{}, err
		}
	} else {
		_, _ = b.syncClone(ctx, SyncOptions{NoPush: true})
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return CompactResult{}, err
	}
	res, err := b.undoLocked(ctx, req)
	return res, releaseKeep(err, lock)
}

// undoLocked is UndoEpoch under the store lock.
func (b *Backend) undoLocked(ctx context.Context, req UndoRequest) (CompactResult, error) {
	h, err := b.head(ctx)
	if err != nil {
		return CompactResult{}, err
	}
	remote, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return CompactResult{}, err
	}
	if !req.DryRun && h != remote {
		return CompactResult{}, errs.New(errs.Contended, "a write landed in this clone while the undo prepared; nothing was changed").
			WithHint("run it again")
	}
	cur := b.epochAt(ctx, h)
	e, cp, err := b.findCheckpoint(ctx, h)
	if err != nil {
		return CompactResult{}, err
	}
	if e == "" || cp.ToEpoch != cur {
		return CompactResult{}, errs.New(errs.NotFound, "store compact: this store's history has no compaction to undo (epoch "+strconv.Itoa(cur)+")")
	}
	if req.Epoch != cp.FromEpoch {
		return CompactResult{}, errs.New(errs.InvalidInput, "store compact: the newest compaction replaced epoch "+strconv.Itoa(cp.FromEpoch)+"; only it can be undone").
			WithHint("pass --undo-epoch " + strconv.Itoa(cp.FromEpoch))
	}
	n, err := b.lines(ctx, "count the writes since the compaction", "rev-list", "--count", e+".."+h)
	if err != nil {
		return CompactResult{}, err
	}
	res := CompactResult{FromEpoch: cur, ToEpoch: cur + 1, Archive: archiveRef(cur)}
	if len(n) == 1 {
		res.Kept, _ = strconv.Atoi(n[0])
	}
	if !b.haveOrFetch(ctx, cp.OldTip, cp) {
		return CompactResult{}, errs.New(errs.Offline, "store compact: the archived history "+cp.Archive+" could not be fetched; nothing was changed").
			WithHint("run it again when the store (or its archive_url) is reachable")
	}
	if req.DryRun {
		return res, nil
	}
	if _, _, err := b.compactorCheck(ctx, h); err != nil {
		return CompactResult{}, err
	}
	newTip, idMap, err := b.remakeOnto(ctx, e, h, cp.OldTip)
	if err != nil {
		return CompactResult{}, err
	}
	m, err := b.markerAt(ctx, h)
	if err != nil {
		return CompactResult{}, err
	}
	m.Epoch = res.ToEpoch
	ucp := Checkpoint{V: checkpointVersion, Kind: CheckpointUndo, FromEpoch: cur, ToEpoch: res.ToEpoch, OldTip: h, Archive: res.Archive, ArchiveURL: m.ArchiveURL, Restore: cp.OldTip}
	tip, err := b.epochCommitV2(ctx, newTip, m, ucp)
	if err != nil {
		return CompactResult{}, err
	}
	return b.publishRewrite(ctx, res, h, remote, tip, "", idMap)
}

// VerifyResult is `lore store compact --verify`.
type VerifyResult struct {
	// Epoch is the clone's epoch; 0 = never compacted (nothing to verify).
	Epoch int
	// Verified is true when every compaction back to epoch 0 re-does
	// exactly from its archive; Reason says why not.
	Verified bool
	Reason   string
	// Kind, OldTip and Archive describe the newest checkpoint.
	Kind    string
	OldTip  string
	Archive string
}

// VerifyEpoch checks the clone's compacted history against the archive:
// it fetches each archived history, replays its roster and re-does the
// rewrite; the fresh-clone proof of what a fresh clone otherwise trusts.
func (b *Backend) VerifyEpoch(ctx context.Context) (VerifyResult, error) {
	if err := b.usable(ctx); err != nil {
		return VerifyResult{}, err
	}
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return VerifyResult{}, err
	}
	// a higher epoch on the remote (the one a sync refused, or not taken
	// yet) is checked the way a sync checks it, against this clone's own
	// history; else the clone's own history against its archives
	target, own := h, ""
	if remote, rerr := b.resolve(ctx, b.work, trackingRefNS+b.branch); rerr == nil && remote != "" && b.epochAt(ctx, remote) > b.epochAt(ctx, h) {
		target, own = remote, h
	}
	res := VerifyResult{Epoch: b.epochAt(ctx, target)}
	if res.Epoch == 0 {
		res.Verified = true
		return res, nil
	}
	if _, cp, err := b.findCheckpoint(ctx, target); err != nil {
		return VerifyResult{}, err
	} else {
		res.Kind, res.OldTip, res.Archive = cp.Kind, cp.OldTip, cp.Archive
	}
	_, why, err := b.verifyEpoch(ctx, target, own, 0)
	if err != nil {
		return VerifyResult{}, err
	}
	res.Verified, res.Reason = why == "", why
	return res, nil
}

// moveOwnLoreRefs moves the clone's own signals and live refs off the
// history an adopted epoch replaced (moveOffHistory), with the remote's
// snapshot standing for every squashed commit.
func (b *Backend) moveOwnLoreRefs(ctx context.Context, remote string, proof epochProof) error {
	refs, err := b.otherLoreRefs(ctx)
	if err != nil {
		return err
	}
	snap := ""
	if roots, rerr := b.lines(ctx, "find the snapshot", "rev-list", "--max-parents=0", remote); rerr == nil && len(roots) == 1 {
		snap = roots[0]
	}
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		if strings.HasPrefix(ref, loreTrackingNS) {
			continue
		}
		n, ok, err := b.moveOffHistory(ctx, refs[ref], proof.oldTip, snap, proof.idMap)
		if err != nil {
			return err
		}
		if ok {
			if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", ref, n, refs[ref]}}); err != nil {
				return errs.Wrap(errs.Internal, "git: move "+ref+" onto the adopted history", err)
			}
		}
	}
	return nil
}
