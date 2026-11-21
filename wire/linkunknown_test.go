package wire

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
)

// TestLinkUnknownNoteOnSearchAnswers (devco:D-860, hunt H-128): offline
// past link_index.max_age, a repo linked by its remote is not linked, so
// its folder is not read. A search's answer (and an empty answer, right
// under its searched: line, once) names the folder as "link unknown ...;
// not searched", with the copy's date and the max age, and --json lists
// it under link_unknown. Before, the answer read "searched: me" and
// nothing said a linked folder was dropped. Break-verified by dropping
// the unknownLinkLines line in emptySearchLines and the text edit of
// noteLinkUnknown.
func TestLinkUnknownNoteOnSearchAnswers(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC)
	ctx := withUnknownLinks(background(), []UnknownLink{{Store: "acme/web", Since: since, MaxAge: time.Hour}})
	line := "acme/web: link unknown (offline since 2026-03-04 05:06 UTC, link index older than 1h); not searched; reconnect, then run `lore sync`"
	search := op.Spec{Name: opssearch.OpRun, Access: op.AccessRead}

	res := noteLinkUnknown(ctx, search, staleTextResult(t, "memory  mem_1  a personal note\n"), false)
	text, _ := res.Text()
	if strings.Count(text, line) != 1 {
		t.Fatalf("answer %q does not name the dropped link once (%q)", text, line)
	}
	raw, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		LinkUnknown []linkUnknown `json:"link_unknown"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.LinkUnknown) != 1 || out.LinkUnknown[0] != (linkUnknown{Store: "acme/web", Since: since.Format(time.RFC3339), MaxAge: "1h"}) {
		t.Fatalf("--json link_unknown = %+v (%s)", out.LinkUnknown, raw)
	}

	empty := withEmptyNote(ctx, staleTextResult(t, "(no matches)\n"), emptySearched{Stores: []string{"me"}, Kinds: []string{everyKind}, Words: []string{"vendor"}}, nil)
	empty = noteLinkUnknown(ctx, search, empty, true)
	text, _ = empty.Text()
	if strings.Count(text, line) != 1 {
		t.Fatalf("empty answer names the dropped link %d times, want once under searched:\n%s", strings.Count(text, line), text)
	}
	if i, j := strings.Index(text, "searched:"), strings.Index(text, line); j < i || strings.Contains(text[i:j], "read as:") {
		t.Fatalf("the link unknown line is not right under searched:\n%s", text)
	}

	// a read that lists no searched stores, and a repo with no dropped
	// link, get no line
	if text, _ := noteLinkUnknown(ctx, op.Spec{Name: "memory.list", Access: op.AccessRead}, staleTextResult(t, "x\n"), false).Text(); strings.Contains(text, "link unknown") {
		t.Fatalf("a read that lists no searched stores got the note: %q", text)
	}
	if text, _ := noteLinkUnknown(background(), search, staleTextResult(t, "x\n"), false).Text(); strings.Contains(text, "link unknown") {
		t.Fatalf("no dropped link, yet the note: %q", text)
	}
	if l := unknownLinkLine(UnknownLink{Store: "acme/web", MaxAge: 90 * time.Minute}); !strings.Contains(l, "copy never fetched here") || !strings.Contains(l, "older than 1h30m") {
		t.Fatalf("a copy never fetched reads %q", l)
	}
}

// TestLinkUnknownRefusalNamesTheLink (devco:D-860): a read refused in a
// repo with no lore data ("lore isn't set up here", whose next step was
// `lore init` or `lore org link`) names the dropped link and `lore sync`
// instead, when the repo is linked by its remote and only the link is
// unknown offline; any other refusal, and a repo with no such link, keep
// theirs. Break-verified by returning err at the top of withUnknownLinkErr.
func TestLinkUnknownRefusalNamesTheLink(t *testing.T) {
	t.Parallel()
	ctx := background()
	refusal := noProjectErr(ctx, "/work/web")
	opts := Options{RepoUnknownLinks: func(context.Context) []UnknownLink {
		return []UnknownLink{{Store: "acme/web", Since: time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC), MaxAge: time.Hour}}
	}}
	var e *errs.Error
	if !errors.As(withUnknownLinkErr(ctx, refusal, opts), &e) {
		t.Fatal("not an errs.Error")
	}
	if e.Code != errs.NotAStore || !strings.Contains(e.Message, "acme/web: link unknown (offline since 2026-03-04 05:06 UTC") ||
		strings.Contains(e.Hint, "lore org link") || strings.Contains(e.Hint, "lore init") || !strings.Contains(e.Hint, "`lore sync`") {
		t.Fatalf("refusal %q (hint %q): want the dropped link named and lore sync, never init or org link", e.Message, e.Hint)
	}
	if got := withUnknownLinkErr(ctx, refusal, Options{}); got != refusal {
		t.Fatalf("no dropped link changed the refusal: %v", got)
	}
	other := errs.New(errs.NotFound, "no such row")
	if got := withUnknownLinkErr(ctx, other, opts); got != error(other) {
		t.Fatalf("another refusal changed: %v", got)
	}
}
