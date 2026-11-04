package repourl

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Kind is the closed set of a URL history entry's kinds
// (PROVENANCE_DESIGN 10.2).
type Kind string

// The entry kinds.
const (
	// KindPrimary is the repo's main address (at most one open entry).
	KindPrimary Kind = "primary"
	// KindMirror is another confirmed address of the same repo.
	KindMirror Kind = "mirror"
	// KindUnconfirmed is an address seen on a clone and not yet confirmed
	// (PROV2: trusted only after `lore repo set-url`).
	KindUnconfirmed Kind = "unconfirmed"
)

// How is the closed set of ways an entry was made.
type How string

// The ways an entry is made.
const (
	HowInit    How = "init"
	HowSetURL  How = "set-url"
	HowSeen    How = "seen"
	HowMigrate How = "migrate"
)

// Entry is one address of a repo's history (the repos row's urls field).
// URL is stored through StripCredentials; its identity is Normalize(URL).
// From and To are dates (YYYY-MM-DD); an open entry has no To. By is the
// actor id that made it.
type Entry struct {
	URL  string `json:"url"`
	Kind Kind   `json:"kind"`
	How  How    `json:"how"`
	From string `json:"from"`
	To   string `json:"to,omitempty"`
	By   string `json:"by,omitempty"`
}

// Key is the entry's normalised identity ("" when the URL no longer
// normalises, which only a hand edit can cause).
func (e Entry) Key() string {
	k, err := Normalize(e.URL)
	if err != nil {
		return ""
	}
	return k
}

// Open reports whether the entry is current (no end date).
func (e Entry) Open() bool { return e.To == "" }

// History is a repo's URL history, oldest first.
type History []Entry

// ParseHistory decodes a urls field ("" or null is an empty history).
func ParseHistory(raw []byte) (History, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil, nil
	}
	var h History
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "repourl: the repo's urls field is not a list of entries", err)
	}
	return h, nil
}

// JSON encodes the history for the urls field.
func (h History) JSON() ([]byte, error) {
	if h == nil {
		h = History{}
	}
	b, err := json.Marshal(h)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "repourl: encode urls", err)
	}
	return b, nil
}

// Find is the index of the newest entry whose key is key (open entries
// first), or -1.
func (h History) Find(key string) int {
	found := -1
	for i, e := range h {
		if e.Key() != key {
			continue
		}
		if found < 0 || e.Open() || !h[found].Open() {
			found = i
		}
	}
	return found
}

// Primary is the open primary entry, if any.
func (h History) Primary() (Entry, bool) {
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Kind == KindPrimary && h[i].Open() {
			return h[i], true
		}
	}
	return Entry{}, false
}

// Confirmed is the confirmed addresses as normalised keys, newest first:
// the open primary, then open mirrors, then closed confirmed addresses (an
// old address still identifies the repo for a clone that kept it, 10.4).
// This is the list `_meta.json` urls holds.
func (h History) Confirmed() []string {
	type ranked struct {
		key  string
		rank int
		date string
	}
	var rs []ranked
	for _, e := range h {
		k := e.Key()
		if k == "" || e.Kind == KindUnconfirmed {
			continue
		}
		r := ranked{key: k, rank: 2, date: e.To}
		if e.Open() {
			r.rank, r.date = 1, e.From
			if e.Kind == KindPrimary {
				r.rank = 0
			}
		}
		rs = append(rs, r)
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].rank != rs[j].rank {
			return rs[i].rank < rs[j].rank
		}
		return rs[i].date > rs[j].date
	})
	var out []string
	for _, r := range rs {
		if !slices.Contains(out, r.key) {
			out = append(out, r.key)
		}
	}
	return out
}

// Unconfirmed is every open unconfirmed entry, oldest first.
func (h History) Unconfirmed() []Entry {
	var out []Entry
	for _, e := range h {
		if e.Kind == KindUnconfirmed && e.Open() {
			out = append(out, e)
		}
	}
	return out
}

// CaseVariant is the confirmed key that differs from key only in letter
// case (PROV3), if any.
func (h History) CaseVariant(key string) (string, bool) {
	for _, k := range h.Confirmed() {
		if CaseOnly(k, key) {
			return k, true
		}
	}
	return "", false
}

// Seen records an address a clone uses (PROV2): a key already in the
// history changes nothing (no dates move, so files stay quiet); a new key
// is appended as unconfirmed. added reports the append.
func (h History) Seen(clean, today, by string) (out History, added bool, err error) {
	key, err := Normalize(clean)
	if err != nil {
		return h, false, errs.Wrap(errs.InvalidInput, "repourl: the remote is not a repo address", err)
	}
	if h.Find(key) >= 0 {
		return h, false, nil
	}
	return append(slices.Clone(h), Entry{URL: clean, Kind: KindUnconfirmed, How: HowSeen, From: today, By: by}), true, nil
}

// SetMode is what `lore repo set-url` does with the address.
type SetMode int

// The set-url modes.
const (
	// SetPrimary makes the address the primary (the repo moved or was
	// renamed): the open primary is closed, the address added or promoted.
	SetPrimary SetMode = iota
	// SetMirror adds or promotes the address as a mirror.
	SetMirror
	// SetRemove closes a mirror or drops an unconfirmed entry; closed
	// history is never deleted.
	SetRemove
)

// Set applies `lore repo set-url` (PROVENANCE_DESIGN 10.4). from is the
// entry's start date (today, or --from), today the date an entry is closed.
// changed is false when the history already says so.
func (h History) Set(clean string, mode SetMode, from, today, by string) (out History, changed bool, err error) {
	key, err := Normalize(clean)
	if err != nil {
		return h, false, errs.Wrap(errs.InvalidInput, "repourl: not a repo address", err)
	}
	out = slices.Clone(h)
	i := out.Find(key)
	open := i >= 0 && out[i].Open()
	switch mode {
	case SetRemove:
		if !open {
			return h, false, errs.New(errs.NotFound, "repourl: "+key+" is not a current address of this repo").
				WithHint("run `lore doctor` to see the addresses (Repo addresses)")
		}
		switch out[i].Kind {
		case KindPrimary:
			return h, false, errs.New(errs.InvalidInput, "repourl: "+key+" is the primary address; set another primary first").
				WithHint("lore repo set-url <new address>")
		case KindUnconfirmed:
			return slices.Delete(out, i, i+1), true, nil
		default: // a mirror closes; its history stays
			out[i].To = today
			return out, true, nil
		}
	case SetMirror:
		if open && out[i].Kind == KindMirror {
			return h, false, nil
		}
		if open && out[i].Kind == KindPrimary {
			return h, false, errs.New(errs.InvalidInput, "repourl: "+key+" is already the primary address")
		}
		if open {
			out[i] = Entry{URL: clean, Kind: KindMirror, How: HowSetURL, From: from, By: by}
			return out, true, nil
		}
		return append(out, Entry{URL: clean, Kind: KindMirror, How: HowSetURL, From: from, By: by}), true, nil
	default: // SetPrimary, below
	}
	if open && out[i].Kind == KindPrimary {
		return h, false, nil
	}
	for j := range out {
		if out[j].Kind == KindPrimary && out[j].Open() {
			out[j].To = today
		}
	}
	if open {
		out[i] = Entry{URL: clean, Kind: KindPrimary, How: HowSetURL, From: from, By: by}
		return out, true, nil
	}
	return append(out, Entry{URL: clean, Kind: KindPrimary, How: HowSetURL, From: from, By: by}), true, nil
}
