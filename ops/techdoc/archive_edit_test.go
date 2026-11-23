package techdoc_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
)

// TestTechdocArchiveAndEdit pins devco D-924: a techdoc archives (hidden
// from list and search, kept, unarchive brings it back) and edits in place
// like every other searchable row kind; before, the only way to change
// its base URL was delete and add again.
func TestTechdocArchiveAndEdit(t *testing.T) {
	h := opstest.New(t)
	id := h.ID(h.Must("techdoc.add", `{"name":"Widget API","base_url":"https://docs.example.com/v1"}`))

	if got, want := opstest.Text(h.Must("techdoc.edit", `{"id":"`+id+`","base_url":"https://docs.example.com/v2"}`)), contenttest.Mark+" techdoc "+id+" updated\n"; got != want {
		t.Errorf("edit text %q, want %q", got, want)
	}
	show := h.JSON(h.Must("techdoc.show", `{"id":"`+id+`"}`))
	if !strings.Contains(show, "https://docs.example.com/v2") || !strings.Contains(show, "Widget API") {
		t.Errorf("edit changed the wrong fields: %s", show)
	}
	_, err := h.Run("techdoc.edit", `{"id":"`+id+`"}`)
	contenttest.Fails(t, "edit with no field", err, errs.InvalidInput)

	if got, want := opstest.Text(h.Must("techdoc.archive", `{"id":"`+id+`"}`)), contenttest.Mark+" "+id+" archived\n"; got != want {
		t.Errorf("archive text %q, want %q", got, want)
	}
	if !strings.Contains(h.JSON(h.Must("techdoc.show", `{"id":"`+id+`"}`)), `"archived_at"`) {
		t.Error("show: no archived_at after archive")
	}
	if strings.Contains(opstest.Text(h.Must("techdoc.list", "")), id) {
		t.Error("list shows an archived techdoc")
	}
	if !strings.Contains(opstest.Text(h.Must("techdoc.list", `{"archived":true}`)), id) {
		t.Error("list --archived misses the archived techdoc")
	}
	if strings.Contains(h.JSON(h.Must("techdoc.search", `{"query":"widget"}`)), id) {
		t.Error("search shows an archived techdoc")
	}
	h.Must("techdoc.unarchive", `{"id":"`+id+`"}`)
	if !strings.Contains(opstest.Text(h.Must("techdoc.list", "")), id) {
		t.Error("unarchive did not bring the techdoc back to the list")
	}
}
