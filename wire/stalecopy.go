package wire

// stalecopy.go — devco D-853 (hunt H-110, owner org-setup item 10): a
// read never passes off a stale copy as the store. When a store's remote
// cannot be reached, lore reads the store's local copy, as it always did,
// but `lore search`, `lore ask`, every `lore <kind> search` and `lore
// context` printed those rows (and, on an empty answer, listed the store
// as searched) with no word that the copy may be days old. Each of them
// now says, for every store it read that way:
//
//	<store>: unreachable, results from the copy of <date>
//
// under its answer (an empty answer: right under its searched: line) and
// as the top-level --json list "unreachable" ({store, copy_of}). The date
// is the copy's last fetch that reached the remote.
//
//	Run ─▶ withStaleCopies(ctx) ─▶ the op reads ─▶ storePush.refresh
//	     │                                         (fetch failed, or backing
//	     │                                          off after a failure)
//	     │                                          └▶ noteStaleCopy(ctx, …)
//	     └▶ noteStaleRead: the lines and the --json list
//
// The record rides the command's ctx, so one REST request of `lore api`
// never sees another's.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/op"
)

// UnreachableField is the top-level --json list of the stores a search
// read from a local copy because their remote did not answer.
const UnreachableField = "unreachable"

// staleCopyLayout is how a copy's date prints (always UTC, said so).
const staleCopyLayout = "2006-01-02 15:04"

// staleCopy is one store a read took from its local copy.
type staleCopy struct {
	// Store is the store's name as this repo calls it.
	Store string `json:"store"`
	// CopyOf is when the copy last reached the remote (RFC 3339, UTC);
	// "" when this machine never fetched it.
	CopyOf string `json:"copy_of,omitempty"`
}

// staleCopyInfo declares the field for the OpenAPI document.
var staleCopyInfo = op.FieldInfo{
	Name: UnreachableField, Type: op.TypeList,
	Desc: "the stores whose remote did not answer, read from this machine's copy (absent when every store answered or was fetched lately)",
	Fields: []op.FieldInfo{
		{Name: "store", Type: op.TypeString, Required: true, Desc: "the store's name in this repo"},
		{Name: "copy_of", Type: op.TypeString, Desc: "the copy's last fetch that reached the remote, a UTC time such as 2026-11-21T16:20:00Z; absent when it was never fetched here"},
	},
}

// staleCopies collects the stale stores of one command.
type staleCopies struct {
	mu sync.Mutex
	by map[string]time.Time
}

// staleCopiesKey is the context key of withStaleCopies.
type staleCopiesKey struct{}

// withStaleCopies returns ctx carrying a fresh record of the stores this
// command reads from a stale copy (a ctx that has one keeps it: a nested
// run records into the command's own).
func withStaleCopies(ctx context.Context) context.Context {
	if _, ok := ctx.Value(staleCopiesKey{}).(*staleCopies); ok {
		return ctx
	}
	return context.WithValue(ctx, staleCopiesKey{}, &staleCopies{})
}

// noteStaleCopy records that this command read store from its local copy
// because the remote did not answer; at is the copy's last good fetch
// (zero = never). Without a record in ctx (a read outside Run) it does
// nothing.
func noteStaleCopy(ctx context.Context, store string, at time.Time) {
	s, ok := ctx.Value(staleCopiesKey{}).(*staleCopies)
	if !ok || store == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.by == nil {
		s.by = map[string]time.Time{}
	}
	s.by[store] = at
}

// staleCopiesOf is what ctx recorded, by store name.
func staleCopiesOf(ctx context.Context) []staleCopy {
	s, ok := ctx.Value(staleCopiesKey{}).(*staleCopies)
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]staleCopy, 0, len(s.by))
	for name, at := range s.by {
		c := staleCopy{Store: name}
		if !at.IsZero() {
			c.CopyOf = at.UTC().Format(time.RFC3339)
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(x, y staleCopy) int { return strings.Compare(x.Store, y.Store) })
	return out
}

// staleCopyLine is the line naming one stale store.
func staleCopyLine(c staleCopy) string {
	if c.CopyOf == "" {
		return c.Store + ": unreachable, results from this machine's copy (never fetched here); `lore store doctor` says why"
	}
	at, err := time.Parse(time.RFC3339, c.CopyOf)
	when := c.CopyOf
	if err == nil {
		when = at.UTC().Format(staleCopyLayout) + " UTC"
	}
	return c.Store + ": unreachable, results from the copy of " + when + "; `lore store doctor` says why"
}

// staleSearched is the stores of cs that searched names (a store, one
// of its folders "<store>/<folder>", or "<store> (every folder)"): an
// empty answer names under its searched: line only the stale copies it
// read, never a store it did not search.
func staleSearched(cs []staleCopy, searched []string) []staleCopy {
	var out []staleCopy
	for _, c := range cs {
		if slices.ContainsFunc(searched, func(s string) bool {
			return s == c.Store || strings.HasPrefix(s, c.Store+"/") || strings.HasPrefix(s, c.Store+" ")
		}) {
			out = append(out, c)
		}
	}
	return out
}

// staleCopyLines is the indented lines of cs.
func staleCopyLines(cs []staleCopy) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString("  " + staleCopyLine(c) + "\n")
	}
	return b.String()
}

// noteStaleRead returns res with the stale stores of ctx named, for the
// reads that list what they searched (emptySearchSpec). An empty answer
// names them in its searched block (emptySearchLines, only the stores it
// searched), so only its --json list is added here; a line already in
// the text is not added again.
func noteStaleRead(ctx context.Context, spec op.Spec, res op.Result, empty bool) op.Result {
	if !emptySearchSpec(spec) {
		return res
	}
	cs := staleCopiesOf(ctx)
	if len(cs) == 0 {
		return res
	}
	raw, err := json.Marshal(cs)
	if err != nil {
		return res
	}
	if empty {
		return res.WithTopJSON(UnreachableField, raw)
	}
	res = res.WithTextEdit(func(t string) string {
		var add strings.Builder
		for _, c := range cs {
			if l := staleCopyLine(c); !strings.Contains(t, l) {
				add.WriteString("  " + l + "\n")
			}
		}
		if add.Len() == 0 {
			return t
		}
		return strings.TrimRight(t, "\n") + "\n" + add.String()
	})
	return res.WithTopJSON(UnreachableField, raw)
}
