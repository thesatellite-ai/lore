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
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
// org file's changes, devco:D-999).
const authorityFormat = 3

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
	Clone    string
	Head     string
	OrgHead  string
	Orgless  bool
	Docs     map[string]savedDoc
	Stamps   map[string]map[string][]savedStamp
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
}

// savedOrgChange is an orgFileChange.
type savedOrgChange struct {
	Commit, Parent, Author, Path, Blob string
	Merge                              bool
}

// savedDoc is a rosterDoc with its content key.
type savedDoc struct {
	Doc     rosterDoc
	Content string
}

// savedStamp is a stampRec.
type savedStamp struct{ Actor, Content string }

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
func (b *Backend) loadAuthority() *authority {
	p, bin := b.authorityPath(), binaryStamp()
	if p == "" || bin == "" {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var s savedAuthority
	if gob.NewDecoder(bytes.NewReader(raw)).Decode(&s) != nil || s.Format != authorityFormat || s.Binary != bin || s.Head == "" || s.Clone != filepath.Clean(b.work) {
		return nil
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
	for p, byHLC := range s.Stamps {
		m := make(map[string][]stampRec, len(byHLC))
		for h, recs := range byHLC {
			out := make([]stampRec, len(recs))
			for i, r := range recs {
				out[i] = stampRec{actor: r.Actor, content: r.Content}
			}
			m[h] = out
		}
		st.stamps[p] = m
	}
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
	s, ok := a.saved(bin)
	if !ok {
		return
	}
	s.Clone = filepath.Clean(b.work)
	if os.MkdirAll(filepath.Dir(p), authorityDirMode) != nil {
		return
	}
	var buf bytes.Buffer
	if gob.NewEncoder(&buf).Encode(&s) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), authorityFile+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(buf.Bytes())
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), p) != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	writeGobAtomic(p+authorityRowsSuffix, savedRows{Format: authorityFormat, Binary: bin, Clone: s.Clone, Head: s.Head, OrgHead: s.OrgHead,
		Held: s.Held, Trusted: s.Trusted, Credited: s.Credited})
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
	s := savedAuthority{Format: authorityFormat, Binary: bin, Head: a.head, OrgHead: a.orgHead, Orgless: a.state.orgless,
		Docs: make(map[string]savedDoc, len(a.state.docs)), Stamps: make(map[string]map[string][]savedStamp, len(a.state.stamps)),
		Held: a.held, Trusted: a.trusted, Refused: a.refused, Credited: make(map[string]savedCredit, len(a.credited)),
		Admins: make(map[string]int, len(a.admins)), Accepted: a.accepted}
	for p, d := range a.state.docs {
		s.Docs[p] = savedDoc{Doc: d, Content: d.content}
	}
	for p, byHLC := range a.state.stamps {
		m := make(map[string][]savedStamp, len(byHLC))
		for h, recs := range byHLC {
			out := make([]savedStamp, len(recs))
			for i, r := range recs {
				out[i] = savedStamp{Actor: r.actor, Content: r.content}
			}
			m[h] = out
		}
		s.Stamps[p] = m
	}
	for p, c := range a.credited {
		s.Credited[p] = savedCredit{Actor: c.actor, Claimed: c.claimed, HLC: c.hlc, Created: c.created, Commit: c.commit}
	}
	for _, c := range a.orgFile {
		s.OrgFile = append(s.OrgFile, savedOrgChange{Commit: c.commit, Parent: c.parent, Author: c.author, Path: c.path, Blob: c.blob, Merge: c.merge})
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
		return savedAuthority{}, false
	}
	s.Snap = at(a.snap)
	for id, set := range a.admins {
		if set == nil {
			return savedAuthority{}, false
		}
		s.Admins[id] = at(set)
	}
	return s, true
}
