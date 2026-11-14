package link_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestLinkRemapFollowsARewrite (H-8): after a rebase or an amend the
// commit's link moves to the new sha: commit.show answers for the new
// commit and no longer for the old one, the old link is in the trash, and
// a second remap of the same pair finds nothing. A pair that is not two
// shas is refused.
func TestLinkRemapFollowsARewrite(t *testing.T) {
	h := opstest.New(t)
	seedTask(t, h)
	oldSHA := commit(t, h, "One devbox binary")
	newSHA := commit(t, h, "One devbox binary (rebased)")
	h.Must("link.add", `{"entity":"`+v017Task+`","commit":"`+oldSHA+`","repo_path":`+opstest.Quote(h.Repo)+`}`)
	res := h.Must("link.remap", `{"pairs":["`+oldSHA+`=`+newSHA+`"]}`)
	if got := opstest.Text(res); !strings.Contains(got, "tasks/"+v017Task) || !strings.Contains(got, newSHA[:8]) {
		t.Fatalf("remap text: %q", got)
	}
	if got := opstest.Text(h.Must("commit.show", `{"id":"`+newSHA+`"}`)); !strings.Contains(got, "tasks/"+v017Task) {
		t.Fatalf("the new commit shows no link: %q", got)
	}
	if got := opstest.Text(h.Must("commit.show", `{"id":"`+oldSHA+`"}`)); !strings.Contains(got, "no entities linked") {
		t.Fatalf("the old commit still answers: %q", got)
	}
	if got := h.JSON(h.Must("link.list", `{"commit":"`+newSHA+`"}`)); !strings.Contains(got, "(rebased)") {
		t.Fatalf("the new link lacks the new commit's message: %s", got)
	}
	if got := h.JSON(h.Must("link.remap", `{"pairs":["`+oldSHA+`=`+newSHA+`"]}`)); !strings.Contains(got, `"count":0`) && !strings.Contains(got, `"count": 0`) {
		t.Fatalf("a second remap moved something: %s", got)
	}
	h.Fails("link.remap", `{"pairs":["HEAD=main"]}`, errs.InvalidInput)
}
