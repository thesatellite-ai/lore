package settings_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// hintOf is the hint of a typed error ("" when it has none).
func hintOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

// TestMetaInputRulesSaid (devco:D-922, H-180): every input rule a metadata
// write refuses is said where it is refused, with a fix that works as
// printed: a reserved key name lists the reserved words and a name that
// passes; a value that is not snake_case names the rule and the whole
// flag corrected; a row of a table without metadata lists the tables that
// take it.
func TestMetaInputRulesSaid(t *testing.T) {
	t.Parallel()
	h := multiStore(t, ownRepo)

	_, err := h.RunYes(settings.OpMetaAdd, `{"name":"state","type":"text"}`)
	if !errs.Has(err, errs.MetaReserved) || !strings.Contains(err.Error(), "the type words (") || !strings.Contains(err.Error(), "level words (org, project, repo)") {
		t.Fatalf("reserved name: %v, want the reserved words listed", err)
	}
	if got := hintOf(err); got != "use another name, e.g. --name item_state" {
		t.Fatalf("reserved name hint %q", got)
	}
	h.MustYes(settings.OpMetaAdd, `{"name":"item_state","type":"text"}`) // the suggested name passes

	_, err = h.RunYes(settings.OpMetaAdd, `{"name":"feed_source","type":"text","values":"pylon-feeds,raw,Daily Feed"}`)
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), `"pylon-feeds", "Daily Feed" is not snake_case: a-z, 0-9 and _, starting with a letter`) {
		t.Fatalf("not snake_case: %v", err)
	}
	if got := hintOf(err); got != "use --values prism_feeds,raw,daily_feed" {
		t.Fatalf("not snake_case hint %q", got)
	}
	h.MustYes(settings.OpMetaAdd, `{"name":"feed_source","type":"text","values":"prism_feeds,raw,daily_feed"}`)
	_, err = h.RunYes(settings.OpMetaValueAdd, `{"key":"feed_source","value":"night-feed"}`)
	if got := hintOf(err); got != "use --value night_feed" {
		t.Fatalf("value add hint %q (%v)", got, err)
	}

	tag := h.ID(h.MustYes("tag.add", `{"name":"feeds_team"}`))
	msg := h.Fails(settings.OpMetaSet, `{"id":"`+tag+`","pair":"owner_team=web"}`, errs.MetaTable)
	if !strings.Contains(msg, "metadata goes on rows of ") || !strings.Contains(msg, "memories") || !strings.Contains(msg, "tasks") {
		t.Fatalf("E_META_TABLE does not list the tables: %s", msg)
	}
}

// TestMetaAddNamesOtherDefinition (devco:D-922, H-180): registering a
// key in a folder while the store's own registry holds the same name (or
// the other way round) warns at registration, naming the other definition
// and what a bare meta set will answer; it used to succeed silently and
// every later bare `meta set` on a row that sees both failed
// E_META_AMBIGUOUS. A key with no other definition says nothing.
func TestMetaAddNamesOtherDefinition(t *testing.T) {
	t.Parallel()
	h := multiStore(t, ownRepo)
	first := h.MustYes(settings.OpMetaAdd, `{"name":"channel","type":"text","to":"kb"}`)
	if got := opstest.Text(first); strings.Contains(got, "also registered") {
		t.Fatalf("first registration warned: %q", got)
	}
	kid := h.ID(first)
	got := opstest.Text(h.MustYes(settings.OpMetaAdd, `{"name":"channel","type":"text","to":"kb/web"}`))
	if !strings.Contains(got, `warning: "channel" is also registered in:`) || !strings.Contains(got, kid) || !strings.Contains(got, "E_META_AMBIGUOUS") {
		t.Fatalf("second definition: %q", got)
	}
	h.MustYes(settings.OpMetaAdd, `{"name":"region","type":"text","to":"kb/web"}`)
	if got := opstest.Text(h.MustYes(settings.OpMetaAdd, `{"name":"region","type":"text","to":"kb"}`)); !strings.Contains(got, `"region" is also registered in:`) {
		t.Fatalf("store after folder: %q", got)
	}
}

// TestMetaSetDryRunWritesNothing (devco:D-922, H-180): the one-row
// `meta set --dry-run` wrote the value (and registered the key); it now
// writes nothing and refuses what the write refuses (a reserved name, a
// table without metadata) with the same code.
func TestMetaSetDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	h := multiStore(t, ownRepo)
	id := h.ID(h.MustYes("memory.add", `{"body":"feeds refresh every ten minutes"}`))
	out := opstest.Text(h.Must(settings.OpMetaSet, `{"id":"`+id+`","pair":"release_note=x","dry_run":true}`))
	if !strings.Contains(out, "nothing written (dry run)") {
		t.Fatalf("dry run said %q", out)
	}
	if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+id+`"}`)); strings.Contains(got, "release_note") {
		t.Fatalf("dry run wrote the value: %q", got)
	}
	keys := h.JSON(h.Must(settings.OpMetaKeys, `{}`))
	if strings.Contains(keys, "release_note") {
		t.Fatalf("dry run registered the key: %s", keys)
	}
	tag := h.ID(h.MustYes("tag.add", `{"name":"feeds_team"}`))
	for _, c := range []struct {
		in   string
		code errs.Code
	}{
		{`{"id":"` + id + `","pair":"state=x"}`, errs.MetaReserved},
		{`{"id":"` + tag + `","pair":"owner_team=web"}`, errs.MetaTable},
	} {
		_, real := h.RunYes(settings.OpMetaSet, c.in)
		_, dry := h.RunYes(settings.OpMetaSet, strings.TrimSuffix(c.in, "}")+`,"dry_run":true}`)
		if !errs.Has(real, c.code) || !errs.Has(dry, c.code) {
			t.Errorf("%s: write %v, dry run %v; want both %s", c.in, real, dry, c.code)
		}
	}
}
