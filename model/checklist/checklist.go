// Package checklist is one task's checklist: the items of the task row's
// `checklist` list (TASK_SYSTEM_DESIGN 7.2).
//
// Layer L0: imports stdlib and model only.
//
//	ops/task checklist add|tick|untick|edit|rm|promote|import
//	   └─► Decode(row value) → Live/Resolve → mutate Item → Encode → patch
//	task done ──► Open(list) → E_OPEN_ITEMS unless --tick-all / --force (TSQ14)
//
// Invariants:
//   - ID is permanent and never reused; it is the merge key of the list
//     (logic/merge StrategyListByID): same id on both sides, the newer
//     UpdatedAt wins for the whole item.
//   - An item is never physically removed: rm sets DeletedAt (a tombstone),
//     so a delete on one machine and an edit on another converge.
//   - Positions (1-based) count live items in display order (rank, then
//     id); agents should use chk_ ids, since positions shift after a merge.
package checklist

import (
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Tag tags ids of checklist items (prefix "chk").
type Tag struct{}

// Prefix implements ids.Tag.
func (Tag) Prefix() ids.Prefix { return ids.PrefixChecklistItem }

// Limits of TASK 7.2.
const (
	// MaxItems is the most live items one task holds.
	MaxItems = 100
	// MaxText is the longest item text, in characters.
	MaxText = 200
)

// TimeLayout is the text form of every time in an item (UTC RFC3339).
const TimeLayout = time.RFC3339

// Item is one checklist item.
type Item struct {
	ID        ids.ID[Tag] `json:"id"`
	Text      string      `json:"text"`
	Done      bool        `json:"done"`
	DoneAt    string      `json:"done_at,omitempty"`
	DoneBy    string      `json:"done_by,omitempty"`
	Rank      string      `json:"rank"`
	CreatedAt string      `json:"created_at"`
	CreatedBy string      `json:"created_by,omitempty"`
	UpdatedAt string      `json:"updated_at"`
	UpdatedBy string      `json:"updated_by,omitempty"`
	DeletedAt string      `json:"deleted_at,omitempty"`
	DeletedBy string      `json:"deleted_by,omitempty"`
}

// Deleted reports a tombstoned item.
func (it Item) Deleted() bool { return it.DeletedAt != "" }

// CleanText validates one item text: trimmed, one line, no control
// characters, at most MaxText characters, not empty.
func CleanText(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errs.New(errs.InvalidInput, "checklist item text is empty")
	}
	if utf8.RuneCountInString(s) > MaxText {
		return "", errs.New(errs.InvalidInput, "checklist item is longer than "+strconv.Itoa(MaxText)+" characters").
			WithHint("keep an item to one short line; longer work is a sub-task")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", errs.New(errs.InvalidInput, "checklist item must be one line without control characters")
		}
	}
	return s, nil
}

// New builds a fresh item with text, rank and the creator stamp.
func New(text, rank, by string, now time.Time) (Item, error) {
	t, err := CleanText(text)
	if err != nil {
		return Item{}, err
	}
	id, err := ids.New[Tag]()
	if err != nil {
		return Item{}, err
	}
	stamp := now.UTC().Format(TimeLayout)
	return Item{ID: id, Text: t, Rank: rank, CreatedAt: stamp, CreatedBy: by, UpdatedAt: stamp, UpdatedBy: by}, nil
}

// Touch stamps an edit of it by by at now.
func (it *Item) Touch(by string, now time.Time) {
	it.UpdatedAt = now.UTC().Format(TimeLayout)
	it.UpdatedBy = by
}

// SetDone ticks (done=true) or unticks the item.
func (it *Item) SetDone(done bool, by string, now time.Time) {
	it.Done = done
	if done {
		it.DoneAt, it.DoneBy = now.UTC().Format(TimeLayout), by
	} else {
		it.DoneAt, it.DoneBy = "", ""
	}
	it.Touch(by, now)
}

// Remove tombstones the item.
func (it *Item) Remove(by string, now time.Time) {
	it.DeletedAt, it.DeletedBy = now.UTC().Format(TimeLayout), by
	it.Touch(by, now)
}

// Decode parses a checklist list (JSON array). Empty input is no items.
func Decode(raw []byte) ([]Item, error) {
	if len(strings.TrimSpace(string(raw))) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out []Item
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "checklist is not a list of checklist items", err)
	}
	return out, nil
}

// Encode renders the list in display order (tombstones kept).
func Encode(list []Item) ([]byte, error) {
	sorted := slices.Clone(list)
	sortItems(sorted)
	raw, err := json.Marshal(sorted)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "checklist: encode", err)
	}
	return raw, nil
}

// sortItems orders by rank, ties by id (the tree's sibling rule).
func sortItems(xs []Item) {
	slices.SortStableFunc(xs, func(a, b Item) int {
		return cmp.Or(cmp.Compare(a.Rank, b.Rank), cmp.Compare(a.ID.String(), b.ID.String()))
	})
}

// Live returns the live items in display order.
func Live(list []Item) []Item {
	var out []Item
	for _, it := range list {
		if !it.Deleted() {
			out = append(out, it)
		}
	}
	sortItems(out)
	return out
}

// Open returns the live items not ticked yet, in display order.
func Open(list []Item) []Item {
	var out []Item
	for _, it := range Live(list) {
		if !it.Done {
			out = append(out, it)
		}
	}
	return out
}

// LastRank is the rank of the last live item ("" when none).
func LastRank(list []Item) string {
	live := Live(list)
	if len(live) == 0 {
		return ""
	}
	return live[len(live)-1].Rank
}

// Resolve finds the index into list of the item ref names: a chk_ id
// (live or tombstoned) or a 1-based position among the live items.
func Resolve(list []Item, ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, string(ids.PrefixChecklistItem)+"_") {
		for i, it := range list {
			if it.ID.String() == ref {
				return i, nil
			}
		}
		return -1, errs.New(errs.NotFound, "no checklist item "+strconv.Quote(ref))
	}
	n, err := strconv.Atoi(ref)
	if err != nil {
		return -1, errs.New(errs.InvalidInput, "checklist item "+strconv.Quote(ref)+": give a position (1, 2, …) or a chk_ id")
	}
	live := Live(list)
	if n < 1 || n > len(live) {
		return -1, errs.New(errs.NotFound, "no checklist item at position "+ref+" (the task has "+strconv.Itoa(len(live))+")").
			WithHint("lore task checklist list <task> shows the positions and ids")
	}
	want := live[n-1].ID
	for i, it := range list {
		if it.ID == want {
			return i, nil
		}
	}
	return -1, errs.New(errs.Internal, "checklist: live item not in list")
}

// Progress is "done/total" over the live items.
func Progress(list []Item) (done, total int) {
	for _, it := range Live(list) {
		total++
		if it.Done {
			done++
		}
	}
	return done, total
}

// BodyItem is one `- [ ]` / `- [x]` line found in a task body.
type BodyItem struct {
	Text string
	Done bool
}

// FromBody parses the markdown task lines of a body (TASK 7.4 import):
// `- [ ] text`, `- [x] text`, `* [X] text`, nested bullets included;
// lines inside fenced code blocks are ignored.
func FromBody(body string) []BodyItem {
	var out []BodyItem
	fence := false
	for line := range strings.SplitSeq(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
			continue
		}
		if fence || len(t) < 6 || (t[0] != '-' && t[0] != '*' && t[0] != '+') || t[1] != ' ' || t[2] != '[' || t[4] != ']' {
			continue
		}
		var done bool
		switch t[3] {
		case ' ':
		case 'x', 'X':
			done = true
		default:
			continue
		}
		text := strings.TrimSpace(t[5:])
		if text == "" {
			continue
		}
		out = append(out, BodyItem{Text: text, Done: done})
	}
	return out
}
