package ask

// staleclaim.go — a row whose "planned / not yet" claim a newer row on
// the same subject contradicts (devco:D-1027, bug hunt H-289): the answer
// marks it like a stale fact ("stale: a newer row says it is done: <id>
// (<day>)", Row.ContradictedBy) and ranks it below a fresh row with the
// same match through that same mark. The row is never dropped: the newer
// row may be the wrong one, the reader decides with lore show --id.
//
//	after the lens: the call's corpus (every live row) and its BM25
//	postings (the meaning view's index: which rows hold a word)
//	  ─► corpus.staleClaims: staleclaim FindAll, once per kept corpus and
//	     set of fact states (question-independent)
//	  └─ the rows the ask read, looked up ─► textRow.Stale, textRow.NewerBy

import (
	"hash/maphash"

	"github.com/thesatellite-ai/lore/logic/staleclaim"
)

// markContradicted marks each row of rows that a newer live row of c
// contradicts; v is the call's meaning view of c (nil: none; its index's
// postings then are not used and each word is looked for in every row),
// facts gives each row's fact_state (nil: none). A row already history
// (superseded, past valid_until) neither is marked nor contradicts
// another: it is printed as history already.
func markContradicted(c *corpus, v *meaningView, rows []textRow, facts map[string]factItems) {
	if c == nil || len(rows) == 0 {
		return
	}
	by := c.staleClaims(v, facts)
	if len(by) == 0 {
		return
	}
	for i := range rows {
		f, ok := by[rows[i].ID]
		if !ok || rows[i].SupersededBy != "" || !rows[i].ValidUntil.IsZero() {
			continue
		}
		rows[i].NewerBy = f.By
		if rows[i].Stale == "" {
			rows[i].Stale = f.Note()
		} else {
			rows[i].Stale += "; " + f.Note()
		}
	}
}

// staleClaims is every stale-claim finding among c's rows by the older
// row's id, found once per corpus and set of fact states (corpus.claims).
func (c *corpus) staleClaims(v *meaningView, facts map[string]factItems) map[string]staleclaim.Finding {
	key := stateKey(c, facts)
	c.claimMu.Lock()
	defer c.claimMu.Unlock()
	if c.claimOK && c.claimKey == key {
		return c.claims
	}
	// one doc per corpus row, in its order: the index's postings name rows
	// by their place
	docs := make([]staleclaim.Doc, len(c.rows))
	for i := range c.rows {
		r := &c.rows[i]
		docs[i] = staleclaim.Doc{ID: r.ID, Title: r.Title, Body: r.Body, At: r.At, State: facts[r.ID].state, History: r.SupersededBy != "" || !r.ValidUntil.IsZero()}
	}
	var holders staleclaim.Holders
	if v != nil && v.ci != nil && v.ci.ix != nil && v.ci.ix.Len() == len(docs) {
		holders = v.ci.ix.Holders
	}
	by := map[string]staleclaim.Finding{}
	for _, f := range staleclaim.NewIndex(docs, holders).FindAll() {
		if _, dup := by[f.ID]; !dup {
			by[f.ID] = f
		}
	}
	c.claims, c.claimKey, c.claimOK = by, key, true
	return by
}

// stateKey hashes the fact_state of c's rows that have one: a change of
// one (a row moved to planned or live) finds the claims again.
func stateKey(c *corpus, facts map[string]factItems) uint64 {
	var h maphash.Hash
	h.SetSeed(corpusSeed)
	for i := range c.rows {
		if st := facts[c.rows[i].ID].state; st != "" {
			h.WriteString(c.rows[i].ID)
			h.WriteByte(0)
			h.WriteString(st)
			h.WriteByte(0)
		}
	}
	return h.Sum64()
}
