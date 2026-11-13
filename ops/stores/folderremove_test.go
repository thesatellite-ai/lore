package stores

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestFolderRemoveWords: the refusal names the counts and every next
// step; the question and the result name the push mode, and a purge's
// result says how to get the rows back.
func TestFolderRemoveWords(t *testing.T) {
	c := FolderContents{Store: "acme", Folder: "client_a", ProjectID: "prj_1", Rows: 3, Trashed: 1, Links: 2, Push: "batch"}
	var e *errs.Error
	if err := notEmptyErr(c); !errors.As(err, &e) || e.Code != errs.Refused || !strings.Contains(e.Message, "4 rows (1 in the trash) and 2 repo links") {
		t.Fatalf("refusal: %v", err)
	}
	for _, want := range []string{"lore move", "lore org unlink --org <org>", "--project client_a", "--purge"} {
		if !strings.Contains(e.Hint, want) {
			t.Errorf("hint %q lacks %q", e.Hint, want)
		}
	}
	if q := removeQuestion(c); !strings.Contains(q, "trash") || !strings.Contains(q, "next batch push") {
		t.Errorf("question %q", q)
	}
	empty := FolderContents{Store: "acme", Folder: "x", ProjectID: "prj_2", Push: "now"}
	if !empty.Empty() || !strings.Contains(removeQuestion(empty), "pushed at once") {
		t.Errorf("empty question %q", removeQuestion(empty))
	}
	if links := (FolderContents{Links: 1}); links.Empty() || !strings.Contains(notEmptyErr(links).Error(), "1 repo link") {
		t.Errorf("a folder with only a link must be refused naming it: %v", notEmptyErr(links))
	}
	out := RemovedOut(c)
	out.Batch, out.Note = "bat_1", "committed here"
	text := out.Text()
	for _, want := range []string{"removed folder client_a (prj_1) from acme", "4 rows", "lore trash restore --target bat_1", "lore store folder add --store acme --folder client_a", "committed here"} {
		if !strings.Contains(text, want) {
			t.Errorf("text %q lacks %q", text, want)
		}
	}
	if !strings.Contains(RemovedOut(empty).Text(), "removed folder x") || strings.Contains(RemovedOut(empty).Text(), "trash") {
		t.Errorf("empty text %q", RemovedOut(empty).Text())
	}
}
