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
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// authorityFile is the replay's file in the clone's git folder.
const authorityFile = "lore-authority.gob"

// authorityFormat is the layout of authorityFile: bump it when
// savedAuthority changes shape.
const authorityFormat = 1

// savedAuthority is an authority as authorityFile holds it (gob encodes
// exported fields only, so each part has an exported twin here).
type savedAuthority struct {
	Format   int
	Binary   string
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

// authorityPath is authorityFile of the clone ("" when its git folder is
// not a plain .git directory).
func (b *Backend) authorityPath() string {
	gd, ok := plainGitDir(b.work)
	if !ok {
		return ""
	}
	return filepath.Join(gd, authorityFile)
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
	if gob.NewDecoder(bytes.NewReader(raw)).Decode(&s) != nil || s.Format != authorityFormat || s.Binary != bin || s.Head == "" {
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
	}
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
