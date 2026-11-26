package git

// authoritydisk.go — the roster replay kept on disk (devco:D-797).
//
// authorityOf replays every commit of a clone and parses every row
// version it ever held: at 5,000 rows that was 0.3 s on every command,
// since the replay was kept in memory only. The result now also lives in
// the clone's git folder, stamped with what it was computed from:
//
//	<clone>/.git/lore-authority.gob
//	  format   ─ authorityFormat (the file's layout)
//	  binary   ─ the lore executable (path, size, mtime): a new lore may
//	             judge commits by new rules, so it replays from scratch
//	  head     ─ the clone HEAD the replay reached
//	  org head ─ the org roster's HEAD a client store was checked against
//
// A read whose stamps all match takes the file as is; one whose HEAD
// moved on by plain commits replays only those (replayFromDisk). A file
// that is missing, unreadable, of another lore or another org head is
// passed over: the full replay runs and writes a new one. Nothing here
// is the truth: it is the replay's own result, recomputed from git when
// in doubt, and a write failure only costs the next process a replay.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// authorityFile is the replay's file in the git folder of a clone lore owns.
const authorityFile = "lore-authority.gob"

// AuthorityCacheDir is the folder under the lore home's cache/ that holds
// the replay of each clone lore does not own (a person's own clone, one
// lore may not write). Why exported: the home layout names it.
const AuthorityCacheDir = "authority"

// homeCacheDir is the cache root under the lore home.
const homeCacheDir = "cache"

// authorityExt ends a replay file under AuthorityCacheDir.
const authorityExt = ".gob"

// authorityDirMode is the mode of the AuthorityCacheDir folders (owner
// only: the replay holds the store's roster).
const authorityDirMode = 0o700

// authorityHashLen is how many bytes of the clone path's hash name its
// file under AuthorityCacheDir.
const authorityHashLen = 8

// authorityFormat is the layout of authorityFile: bump it when
// savedAuthority changes shape (2: the clone path, devco:D-812; 3: the
// org file's changes, devco:D-999; 4: the hold log, devco:D-1004; 5:
// stamp lists per file and 16-byte content keys, devco:D-1017; 6: one
// record per file in the parts, with binary ids, devco:D-1018).
const authorityFormat = 6

// authorityFiles maps a clone folder to the replay file its open chose
// (authorityFileFor), so a Backend made without an open uses the same
// file, never one in a clone lore does not own.
var authorityFiles sync.Map // clean clone path → file path ("" = none)

// savedAuthority is an authority as authorityFile holds it (gob encodes
// exported fields only, so each part has an exported twin here).
type savedAuthority struct {
	Format int
	Binary string
	// Clone is the clone folder the replay is of: a file under the lore
	// home is used for that clone only
	Clone   string
	Head    string
	OrgHead string
	Orgless bool
	Docs    map[string]savedDoc
	// Stamps is the replay's own stamp lists (rosterState.stamps), shared
	// with it, not copied; the file holds them, Admins, Accepted and
	// Refused in savedParts after the header (writeAuthority)
	Stamps   map[string][]stampRec
	Held     map[string]HeldChange
	Trusted  map[string]string
	Refused  map[string]bool
	Credited map[string]savedCredit
	// Snaps holds each distinct admin set once; Admins and Snap index it
	// (the replay shares one set between the commits that kept it)
	Snaps    []savedAdmins
	Admins   map[string]int
	Snap     int
	Accepted map[string]string
	// OrgFile is every change to an org file, in the replay's order
	OrgFile []savedOrgChange
	// Holds is the hold log (authority.holds), in the replay's order
	Holds []savedHold
}

// savedHold is a holdEvent.
type savedHold struct {
	Commit, Path, Trusted string
	Held                  bool
}

// savedOrgChange is an orgFileChange.
type savedOrgChange struct {
	Commit, Parent, Author, Path, Blob string
	Merge                              bool
}

// savedDoc is a rosterDoc with its content key.
type savedDoc struct {
	Doc     rosterDoc
	Content contentSum
}

// savedCredit is a credit.
type savedCredit struct {
	Actor, Claimed, HLC, Commit string
	Created                     bool
}

// savedAdmins is an adminSet.
type savedAdmins struct {
	Emails   map[string]bool
	AnyAdmin bool
}

// binaryStamp names the running lore executable ("" when it cannot be
// told: nothing is then read or written).
var binaryStamp = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s|%d|%d", exe, st.Size(), st.ModTime().UnixNano())
})

// authorityFileFor is where the replay of the clone at dir is kept: its
// git folder when lore owns the clone (outside false), else a file under
// the lore home's cache ("" when home is not known, or dir's git folder is
// not a plain .git directory: nothing is kept then). Why: a read must
// never write into a clone a person owns (devco:D-812).
func authorityFileFor(dir, home string, outside bool) string {
	gd, ok := plainGitDir(dir)
	if !ok {
		return ""
	}
	if !outside {
		return filepath.Join(gd, authorityFile)
	}
	if home == "" {
		return ""
	}
	top := filepath.Dir(gd)
	sum := sha256.Sum256([]byte(top))
	return filepath.Join(home, homeCacheDir, AuthorityCacheDir, filepath.Base(top)+"-"+hex.EncodeToString(sum[:authorityHashLen])+authorityExt)
}

// savedAuthorityFile is the replay file the opens of the clone at dir
// keep (authorityFileFor), for a reader that has no open of it in this
// process (cloneAuthority: AdminAt, OrgFileChanges); "" when it cannot be
// told or nothing is saved there yet, and the replay stays in memory as
// before. Only a clone in a lore home's stores folder is placed (its home
// and clone record say whether it is a person's own); only a file that
// exists is used, so a reader never starts one in a clone lore did not.
// Why (bug hunt H-274, devco:D-1012): lore doctor checks the org file
// before any store open, and that check replayed the whole history again
// in memory on every run, reading every row version: 77 s and 2.1 GB at
// 100k commits, 21+ minutes and 3.3 GB at 1M.
func savedAuthorityFile(dir string) string {
	home, ok := storesHomeOf(dir)
	if !ok {
		return ""
	}
	own := false
	clean := filepath.Clean(dir)
	for _, e := range ReadCloneRecord(home).Clones {
		if e.Why == WhyOwn && filepath.Clean(e.Path) == clean {
			own = true
		}
	}
	p := authorityFileFor(dir, home, own)
	if p == "" {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// keepAuthorityAt records file as the replay file of b's clone (an open's
// choice, authorityFileFor).
func (b *Backend) keepAuthorityAt(file string) {
	b.authorityAt = file
	authorityFiles.Store(filepath.Clean(b.work), file)
}

// authorityPath is the replay file of b's clone: the one its open chose,
// else the one an open of the same clone chose in this process ("" =
// none: the replay stays in memory).
func (b *Backend) authorityPath() string {
	if b.authorityAt != "" {
		return b.authorityAt
	}
	if p, ok := authorityFiles.Load(filepath.Clean(b.work)); ok {
		if s, ok := p.(string); ok {
			return s
		}
	}
	return ""
}

// authorityReadPath is the replay file b reads: authorityPath, else the
// read-only one a temporary backend was given (authorityReadAt). Why
// (devco:D-1004): the org file check at every open replays through a
// temporary backend; before any open of the clone in the process it
// replayed the whole history in memory, and that replay (of a compacted
// history: from the snapshot) replaced the one carried over the
// compaction.
func (b *Backend) authorityReadPath() string {
	if p := b.authorityPath(); p != "" {
		return p
	}
	return b.authorityReadAt
}

// heldSeenPath is the heldSeenFile of the folder named folder, kept where
// the replay is (authorityPath): in the git folder of a clone lore owns,
// else beside the clone's replay file in the lore home ("" = none, the
// holds are served again). Why: like the replay, a read never writes it
// into a clone a person owns (devco:D-812).
func (b *Backend) heldSeenPath(folder string) string {
	p := b.authorityPath()
	switch {
	case p == "":
		return ""
	case filepath.Base(p) == authorityFile:
		return filepath.Join(filepath.Dir(p), heldSeenFile+folder)
	default:
		return strings.TrimSuffix(p, authorityExt) + "." + heldSeenFile + folder
	}
}

// loadAuthority is the replay authorityFile holds, nil when there is none
// this lore can use.
func (b *Backend) loadAuthority() *authority { return b.loadAuthorityAt(b.authorityPath()) }

// authorityBaseSuffix names the replay kept at the remote tip beside the
// saved replay: <replay file>.base. Why (devco:D-1000): a push squashes
// the commits waiting in the clone (pushmodes.go squashOnto), which
// rewrites them, so the replay saved at the old HEAD no longer lies on
// the new history and the next command would replay the whole history
// (D-999 carries a replay only along the first-parent line). The squash
// builds on the remote tip, so the replay at that tip does lie on it, and
// the next command carries it over the squashed commits only.
const authorityBaseSuffix = ".base"

// keepBaseAt keeps the replay this process holds as the replay at the
// remote tip when it reached tip, a commit a push just made the remote's
// (the push's own check judged it before the tracking ref moved, so
// saveAuthority could not tell it was the tip then).
func (b *Backend) keepBaseAt(tip string) {
	authorityCache.Lock()
	a := authorityCache.byWork[b.work]
	authorityCache.Unlock()
	p, bin := b.authorityPath(), binaryStamp()
	if a == nil || a.head != tip || p == "" || bin == "" {
		return
	}
	s, index, ok := a.savedHeader(bin)
	if !ok {
		return
	}
	s.Clone = filepath.Clean(b.work)
	writeAuthority(p+authorityBaseSuffix, a, &s, index)
}

// replayFileDecodes counts the saved replays decoded whole in this
// process: the tests' proof that a reader at the saved HEAD decodes none
// (bug hunt H-277, devco:D-1015).
var replayFileDecodes atomic.Int64

// loadAuthorityAt is the replay saved at p ("" = none), nil when there is
// none this lore can use.
func (b *Backend) loadAuthorityAt(p string) *authority {
	bin := binaryStamp()
	if p == "" || bin == "" {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	replayFileDecodes.Add(1)
	var s savedAuthority
	// decoded from the file as it is read, never read whole first: at 1M
	// commits the file is hundreds of MB (bug hunt H-279, devco:D-1017)
	dec := gob.NewDecoder(bufio.NewReaderSize(f, authorityIOBuf))
	if dec.Decode(&s) != nil || s.Format != authorityFormat || s.Binary != bin || s.Head == "" || s.Clone != filepath.Clean(b.work) {
		return nil
	}
	s.Stamps, s.Admins, s.Accepted, s.Refused = orEmpty(s.Stamps), orEmpty(s.Admins), orEmpty(s.Accepted), orEmpty(s.Refused)
	for {
		var part savedPart
		if dec.Decode(&part) != nil {
			return nil // a file cut short: replay again
		}
		if !readFiles(&part, s.Stamps, s.Accepted) {
			return nil
		}
		maps.Copy(s.Admins, part.Admins)
		maps.Copy(s.Refused, part.Refused)
		if part.Last {
			break
		}
	}
	return s.authority()
}

// authority is the replay s holds, nil when s does not hold together.
func (s savedAuthority) authority() *authority {
	if s.Snap < 0 || s.Snap >= len(s.Snaps) {
		return nil
	}
	snaps := make([]*adminSet, len(s.Snaps))
	for i, x := range s.Snaps {
		snaps[i] = &adminSet{emails: x.Emails, anyAdmin: x.AnyAdmin}
		if snaps[i].emails == nil {
			snaps[i].emails = map[string]bool{}
		}
	}
	st := newRosterState()
	st.orgless = s.Orgless
	for p, d := range s.Docs {
		d.Doc.content = d.Content
		st.docs[p] = d.Doc
	}
	st.stamps = orEmpty(s.Stamps)
	a := &authority{head: s.Head, orgHead: s.OrgHead, state: st, held: orEmpty(s.Held), trusted: orEmpty(s.Trusted), refused: orEmpty(s.Refused),
		credited: map[string]credit{}, admins: make(map[string]*adminSet, len(s.Admins)), accepted: orEmpty(s.Accepted), snap: snaps[s.Snap]}
	for p, c := range s.Credited {
		a.credited[p] = credit{actor: c.Actor, claimed: c.Claimed, hlc: c.HLC, created: c.Created, commit: c.Commit}
	}
	for id, i := range s.Admins {
		if i < 0 || i >= len(snaps) {
			return nil
		}
		a.admins[id] = snaps[i]
	}
	for _, c := range s.OrgFile {
		a.orgFile = append(a.orgFile, orgFileChange{commit: c.Commit, parent: c.Parent, author: c.Author, path: c.Path, blob: c.Blob, merge: c.Merge})
	}
	for _, h := range s.Holds {
		a.holds = append(a.holds, holdEvent{commit: h.Commit, path: h.Path, held: h.Held, trusted: h.Trusted})
	}
	return a
}

// orEmpty is m, or an empty map when the file held none (gob drops empty maps).
func orEmpty[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return map[K]V{}
	}
	return m
}

// saveAuthority writes a to authorityFile (write to a temp file, then
// rename: a reader sees the old file or the new one, never half of one).
// A failure is passed over: the next process replays again.
func (b *Backend) saveAuthority(a *authority) {
	p, bin := b.authorityPath(), binaryStamp()
	if p == "" || bin == "" || a == nil {
		return
	}
	s, index, ok := a.savedHeader(bin)
	if !ok {
		return
	}
	s.Clone = filepath.Clean(b.work)
	if os.MkdirAll(filepath.Dir(p), authorityDirMode) != nil {
		return
	}
	if !writeAuthority(p, a, &s, index) {
		return
	}
	writeGobAtomic(p+authorityRowsSuffix, savedRows{Format: authorityFormat, Binary: bin, Clone: s.Clone, Head: s.Head, OrgHead: s.OrgHead,
		Held: s.Held, Trusted: s.Trusted, Credited: s.Credited})
	b.saveHeadView(a, s)
	if tip, ok := RefFromFiles(b.work, trackingRefNS+b.branch); ok && tip == s.Head {
		copyFileAtomic(p, p+authorityBaseSuffix) // the replay at the remote tip (authorityBaseSuffix)
	}
}

// authorityIOBuf is the buffer a saved replay is written and read through.
const authorityIOBuf = 1 << 16

// savedPart is one part of a saved replay's files, admins and refusals,
// written after its header (writeAuthority); the last part is marked
// Last. Files holds one record per row file (its stamps and accepted
// version together), each naming its folder and actors by their place in
// the part's own Dirs and Actors. Why (bug hunt H-280, devco:D-1018): the
// replay of a compacted store holds every row file of the store, and with
// each path written twice (stamps, accepted), each blob id as hex and each
// stamp's actor id in full, 1.26M files were a 320 MB file.
type savedPart struct {
	Files   []savedFile
	Dirs    []string
	Actors  []string
	Admins  map[string]int
	Refused map[string]bool
	Last    bool
}

// savedFile is one row file of a saved replay: its path (Dirs[Dir] + "/"
// + Base, or Base alone at the top when Dir is -1), its stamps
// (rosterState.stamps) and, when Has, its accepted version
// (authority.accepted: Blob as raw bytes, else Text, "" when removed).
type savedFile struct {
	Dir    int
	Base   string
	Has    bool
	Blob   []byte
	Text   string
	Stamps []savedStamp
}

// savedStamp is a stampRec: its digests as bytes (none for the zero
// sum) and its actor by its place in the part's Actors.
type savedStamp struct {
	HLC, Content []byte
	Actor        int
}

// sumBytes is sum as a savedStamp holds it: nil for the zero sum.
func sumBytes(sum contentSum) []byte {
	if sum == (contentSum{}) {
		return nil
	}
	return sum[:]
}

// sumOf is the contentSum of a savedStamp's bytes; ok is false for bytes
// of another length.
func sumOf(b []byte) (contentSum, bool) {
	var sum contentSum
	if len(b) == 0 {
		return sum, true
	}
	if len(b) != len(sum) {
		return sum, false
	}
	copy(sum[:], b)
	return sum, true
}

// filePart builds the savedParts' file records, with the part's tables.
type filePart struct {
	part   *savedPart
	dirs   map[string]int
	actors map[string]int
}

// add appends p's record: its stamps recs and, when has, its accepted
// version blob.
func (f *filePart) add(p string, recs []stampRec, blob string, has bool) {
	rec := savedFile{Dir: -1, Base: p, Has: has}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		dir := p[:i]
		n, ok := f.dirs[dir]
		if !ok {
			n = len(f.part.Dirs)
			f.dirs[dir] = n
			f.part.Dirs = append(f.part.Dirs, dir)
		}
		rec.Dir, rec.Base = n, p[i+1:]
	}
	if has {
		if raw, err := hex.DecodeString(blob); err == nil && blob != "" && hex.EncodeToString(raw) == blob {
			rec.Blob = raw
		} else {
			rec.Text = blob
		}
	}
	for _, r := range recs {
		n, ok := f.actors[r.Actor]
		if !ok {
			n = len(f.part.Actors)
			f.actors[r.Actor] = n
			f.part.Actors = append(f.part.Actors, r.Actor)
		}
		rec.Stamps = append(rec.Stamps, savedStamp{HLC: sumBytes(r.HLC), Content: sumBytes(r.Content), Actor: n})
	}
	f.part.Files = append(f.part.Files, rec)
}

// readFiles puts part's file records into stamps and accepted; false
// when a record does not hold together (the file is then not used).
func readFiles(part *savedPart, stamps map[string][]stampRec, accepted map[string]string) bool {
	for _, f := range part.Files {
		p := f.Base
		if f.Dir >= len(part.Dirs) || f.Dir < -1 {
			return false
		}
		if f.Dir >= 0 {
			p = part.Dirs[f.Dir] + "/" + f.Base
		}
		if f.Has {
			accepted[p] = f.Text
			if len(f.Blob) > 0 {
				accepted[p] = hex.EncodeToString(f.Blob)
			}
		}
		if len(f.Stamps) == 0 {
			continue
		}
		recs := make([]stampRec, 0, len(f.Stamps))
		for _, st := range f.Stamps {
			hlc, ok1 := sumOf(st.HLC)
			content, ok2 := sumOf(st.Content)
			if !ok1 || !ok2 || st.Actor < 0 || st.Actor >= len(part.Actors) {
				return false
			}
			recs = append(recs, stampRec{HLC: hlc, Actor: part.Actors[st.Actor], Content: content})
		}
		stamps[p] = recs
	}
	return true
}

// authorityPartRows is how many entries (a file's stamps or accepted
// version, a commit's admin set or refusal) one savedPart holds (a var: a
// test writes one per part).
var authorityPartRows = 16384

// writeAuthority writes the replay a to p through a temp file and a
// rename (a reader sees the old file or the new one, never half of one):
// header (a's savedHeader, admin sets indexed by index), then its stamps
// and admins in savedParts, encoded straight into the file. Why (bug hunt
// H-279, devco:D-1017): gob builds each value it encodes whole in memory,
// and the replay of a 1M-commit history is hundreds of MB encoded; it was
// one value, encoded into memory and then copied to the file. false when
// it was not written (the next process replays again).
func writeAuthority(p string, a *authority, header *savedAuthority, index map[*adminSet]int) bool {
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".*")
	if err != nil {
		return false
	}
	w := bufio.NewWriterSize(tmp, authorityIOBuf)
	enc := gob.NewEncoder(w)
	eerr := enc.Encode(header)
	var part savedPart
	var files filePart
	newPart := func() {
		part = savedPart{Admins: map[string]int{}, Refused: map[string]bool{}}
		files = filePart{part: &part, dirs: map[string]int{}, actors: map[string]int{}}
	}
	newPart()
	flush := func(last bool) {
		if eerr != nil || (!last && len(part.Files)+len(part.Admins)+len(part.Refused) < authorityPartRows) {
			return
		}
		part.Last = last
		eerr = enc.Encode(&part)
		newPart()
	}
	for p, recs := range a.state.stamps {
		blob, has := a.accepted[p]
		files.add(p, recs, blob, has)
		flush(false)
	}
	for p, blob := range a.accepted {
		if _, done := a.state.stamps[p]; !done {
			files.add(p, nil, blob, true)
			flush(false)
		}
	}
	for id, set := range a.admins {
		part.Admins[id] = index[set]
		flush(false)
	}
	for id := range a.refused {
		part.Refused[id] = true
		flush(false)
	}
	flush(true)
	ferr := w.Flush()
	if cerr := tmp.Close(); eerr != nil || ferr != nil || cerr != nil || os.Rename(tmp.Name(), p) != nil {
		_ = os.Remove(tmp.Name())
		return false
	}
	return true
}

// copyFileAtomic copies src to dst through a temp file and a rename,
// without holding the file in memory; a failure leaves dst as it was.
func copyFileAtomic(src, dst string) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer func() { _ = in.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*")
	if err != nil {
		return
	}
	_, werr := io.Copy(tmp, in)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), dst) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// authorityRowsSuffix names the row view kept beside a saved replay:
// <replay file>.rows (savedRows).
const authorityRowsSuffix = ".rows"

// savedRows is the part of a saved replay a row read needs (Backend.Get:
// heldBlob and creditRow), kept in a file of its own. Why (devco:D-826):
// the whole replay is ~2 MB of roster state for a store with a long
// history, and decoding it was ~17 ms of every read that looks up one
// actor row (the agent-key settle); the row view is a few hundred bytes.
// It is used only at exactly the head and org head it was saved at, by
// the binary that saved it, so it is always the full replay's answer.
type savedRows struct {
	Format   int
	Binary   string
	Clone    string
	Head     string
	OrgHead  string
	Held     map[string]HeldChange
	Trusted  map[string]string
	Credited map[string]savedCredit
}

// writeBytesAtomic writes raw to p through a temp file and a rename (a
// reader sees the old file or the new one); a failure is passed over.
func writeBytesAtomic(p string, raw []byte) {
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(raw)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), p) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// writeGobAtomic writes v gob-encoded to p through a temp file and a
// rename; a failure leaves no file (the reader then takes the slow path).
func writeGobAtomic(p string, v savedRows) {
	var buf bytes.Buffer
	if gob.NewEncoder(&buf).Encode(&v) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(buf.Bytes())
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), p) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// loadRowView is the saved row view of the replay at head (and the org
// head orgHead), as an authority holding only what heldBlob and
// creditRow read; nil when none matches exactly.
func (b *Backend) loadRowView(head, orgHead string) *authority {
	p, bin := b.authorityPath(), binaryStamp()
	if p == "" || bin == "" || head == "" {
		return nil
	}
	raw, err := os.ReadFile(p + authorityRowsSuffix)
	if err != nil {
		return nil
	}
	var s savedRows
	if gob.NewDecoder(bytes.NewReader(raw)).Decode(&s) != nil || s.Format != authorityFormat || s.Binary != bin ||
		s.Clone != filepath.Clean(b.work) || s.Head != head || s.OrgHead != orgHead {
		return nil
	}
	a := &authority{head: s.Head, orgHead: s.OrgHead, held: s.Held, trusted: s.Trusted, credited: make(map[string]credit, len(s.Credited))}
	for p, c := range s.Credited {
		a.credited[p] = credit{actor: c.Actor, claimed: c.Claimed, hlc: c.HLC, created: c.Created, commit: c.Commit}
	}
	return a
}

// saved is a as authorityFile holds it, stamped with the binary bin; ok
// is false when a has no admin set where the replay stopped (an authority
// not made by replay).
func (a *authority) saved(bin string) (savedAuthority, bool) {
	s, index, ok := a.savedHeader(bin)
	if !ok {
		return savedAuthority{}, false
	}
	s.Stamps, s.Accepted, s.Refused = a.state.stamps, a.accepted, a.refused
	s.Admins = make(map[string]int, len(a.admins))
	for id, set := range a.admins {
		s.Admins[id] = index[set]
	}
	return s, true
}

// savedHeader is saved without the parts that grow with the history,
// Stamps, Admins, Accepted and Refused (writeAuthority writes them in
// parts after it), with the index of each admin set in its Snaps.
func (a *authority) savedHeader(bin string) (savedAuthority, map[*adminSet]int, bool) {
	s := savedAuthority{Format: authorityFormat, Binary: bin, Head: a.head, OrgHead: a.orgHead, Orgless: a.state.orgless,
		Docs: make(map[string]savedDoc, len(a.state.docs)),
		Held: a.held, Trusted: a.trusted, Credited: make(map[string]savedCredit, len(a.credited))}
	for p, d := range a.state.docs {
		s.Docs[p] = savedDoc{Doc: d, Content: d.content}
	}
	for p, c := range a.credited {
		s.Credited[p] = savedCredit{Actor: c.actor, Claimed: c.claimed, HLC: c.hlc, Created: c.created, Commit: c.commit}
	}
	for _, c := range a.orgFile {
		s.OrgFile = append(s.OrgFile, savedOrgChange{Commit: c.commit, Parent: c.parent, Author: c.author, Path: c.path, Blob: c.blob, Merge: c.merge})
	}
	for _, h := range a.holds {
		s.Holds = append(s.Holds, savedHold{Commit: h.commit, Path: h.path, Held: h.held, Trusted: h.trusted})
	}
	index := map[*adminSet]int{}
	at := func(set *adminSet) int {
		if i, ok := index[set]; ok {
			return i
		}
		index[set] = len(s.Snaps)
		s.Snaps = append(s.Snaps, savedAdmins{Emails: set.emails, AnyAdmin: set.anyAdmin})
		return index[set]
	}
	if a.snap == nil {
		return savedAuthority{}, nil, false
	}
	s.Snap = at(a.snap)
	for _, set := range a.admins {
		if set == nil {
			return savedAuthority{}, nil, false
		}
		at(set)
	}
	return s, index, true
}

// authorityViewSuffix names the head view kept beside a saved replay:
// <replay file>.view (savedView).
const authorityViewSuffix = ".view"

// savedView is what a reader of the org file's history needs of the
// replay at exactly its head (OrgFileChanges, AdminAt): the org file
// changes and who was an admin at HEAD, at each change and at each
// change's parent (the commits the org file check and an org file read
// at HEAD or at its accepted change ask about). Why (bug hunt
// H-277, devco:D-1015): lore doctor's org file check decoded the whole
// saved replay, whose stamps (one per row version), accepted versions
// (one per row file) and admin sets (one per commit) grow with the
// history: 51 MB on disk and ~115 MB in memory at 100k commits, ten times
// that at 1M. The view grows only with the org file's own changes.
type savedView struct {
	Format  int
	Binary  string
	Clone   string
	Head    string
	OrgHead string
	OrgFile []savedOrgChange
	// Snaps holds each distinct admin set once; Admins maps HEAD, each org
	// file change and its parent to its set (-1: the replay has none
	// there, the same answer the whole replay gives)
	Snaps  []savedAdmins
	Admins map[string]int
}

// saveHeadView writes the head view of a (saved as s) beside the replay
// file; a failure leaves no view and readers take the whole replay.
func (b *Backend) saveHeadView(a *authority, s savedAuthority) {
	p := b.authorityPath()
	if p == "" {
		return
	}
	v := savedView{Format: authorityFormat, Binary: s.Binary, Clone: s.Clone, Head: s.Head, OrgHead: s.OrgHead, OrgFile: s.OrgFile,
		Admins: map[string]int{}}
	index := map[*adminSet]int{}
	keep := func(commit string) {
		if _, done := v.Admins[commit]; done || commit == "" {
			return
		}
		set, ok := a.admins[commit]
		if !ok || set == nil {
			v.Admins[commit] = -1
			return
		}
		i, seen := index[set]
		if !seen {
			i = len(v.Snaps)
			index[set] = i
			v.Snaps = append(v.Snaps, savedAdmins{Emails: set.emails, AnyAdmin: set.anyAdmin})
		}
		v.Admins[commit] = i
	}
	keep(a.head)
	for _, c := range a.orgFile {
		keep(c.commit)
		keep(c.parent)
	}
	var buf bytes.Buffer
	if gob.NewEncoder(&buf).Encode(&v) != nil {
		_ = os.Remove(p + authorityViewSuffix) // a stale view must not outlive its replay
		return
	}
	writeBytesAtomic(p+authorityViewSuffix, buf.Bytes())
}

// loadHeadView is the saved head view of the replay at head (and the org
// head orgHead), as an authority holding only the org file changes and
// the admin sets savedView keeps (view set); nil when none matches
// exactly. It reads the file a read-only backend was given too
// (authorityReadPath), as replayFromDisk does.
func (b *Backend) loadHeadView(head, orgHead string) *authority {
	p, bin := b.authorityReadPath(), binaryStamp()
	if p == "" || bin == "" || head == "" {
		return nil
	}
	f, err := os.Open(p + authorityViewSuffix)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var s savedView
	if gob.NewDecoder(f).Decode(&s) != nil || s.Format != authorityFormat || s.Binary != bin ||
		s.Clone != filepath.Clean(b.work) || s.Head != head || s.OrgHead != orgHead {
		return nil
	}
	a := &authority{head: s.Head, orgHead: s.OrgHead, view: true, admins: make(map[string]*adminSet, len(s.Admins))}
	sets := make([]*adminSet, len(s.Snaps))
	for i, x := range s.Snaps {
		sets[i] = &adminSet{emails: orEmpty(x.Emails), anyAdmin: x.AnyAdmin}
	}
	for commit, i := range s.Admins {
		switch {
		case i == -1:
			a.admins[commit] = nil // known commit, no admin set: undecided
		case i < 0 || i >= len(sets):
			return nil
		default:
			a.admins[commit] = sets[i]
		}
	}
	for _, c := range s.OrgFile {
		a.orgFile = append(a.orgFile, orgFileChange{commit: c.Commit, parent: c.Parent, author: c.Author, path: c.Path, blob: c.Blob, merge: c.Merge})
	}
	return a
}
