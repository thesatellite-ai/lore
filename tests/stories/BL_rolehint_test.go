//go:build stories && !windows

package stories

// BL_rolehint_test.go — who may choose a branch's notes for everyone in a
// company folder (devco D-697, D-703; owner use pass 5 weak 3 and weak 9):
// a member's branch share is refused with the admin's line naming the
// full place, and that line, run by the admin from their HOME, changes
// exactly that folder; the admin's own share asks for --yes first; an
// undo commits under its own subject.

import (
	"strings"
	"testing"
)

func TestStoryBLRoleHintNamesTheFolder(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	dev, devApp := w.blJoin(blDev)
	place := blOrg + "/" + blFolder // the folder the client repo is linked to
	clone := w.admin.LorePath("stores", "northwind-co", "lore-client-app")
	subject := func() string { return strings.TrimSpace(s.Git(clone, "log", "-1", "--format=%s")) }
	// printed is the lore command printed after marker in out, as args
	printed := func(out, marker string) []string {
		_, rest, ok := strings.Cut(out, marker)
		if !ok {
			t.Fatalf("no %q in:\n%s", marker, out)
		}
		line, _, _ := strings.Cut(rest, "\n")
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "lore" {
			t.Fatalf("no lore command after %q in:\n%s", marker, out)
		}
		return f[1:]
	}
	var fromHome []string
	s.Step("devco:D-697/member-share-names-the-full-folder", func() {
		dev.LoreIn(devApp, "sync").OK(t)
		for _, verb := range []string{"share", "isolate"} {
			r := dev.LoreIn(devApp, "branch", verb, "--branch", "feat/a").
				Refused(t, "only an admin of the org can", "company folder "+place, "Ada Admin",
					"lore branch "+verb+" --branch feat/a --yes in a checkout linked to "+place,
					"--to "+place+" --yes").
				Never(t, "--to "+blOrg+" --yes")
			if verb == "share" {
				fromHome = printed(r.Out(), "or from any folder: ")
			}
			dev.LoreIn(devApp, "branch", verb, "--branch", "feat/a", "--yes").Refused(t, "--to "+place+" --yes")
		}
	})
	s.Step("admin-share-asks-for-yes-first", func() {
		w.admin.LoreIn(w.aapp, "branch", "share", "--branch", "feat/a").
			Refused(t, "E_NEEDS_CONFIRM", "policy set branches.shared in store "+place, "lore branch share --branch feat/a --yes")
		r := w.admin.LoreIn(w.aapp, "branch", "isolate", "--branch", "dev").
			Refused(t, "E_NEEDS_CONFIRM", "lore branch isolate --branch dev --yes")
		// the printed answer runs where it was printed
		w.admin.LoreIn(w.aapp, printed(r.Out(), "rerun with --yes: ")...).OK(t).Says(t, "own notes")
		w.admin.LoreIn(w.aapp, "branch", "isolate", "--branch", "dev", "--undo", "--yes").OK(t)
		w.admin.LoreIn(w.aapp, "branch", "mode", "--branch", "feat/a").OK(t).Never(t, "policy branches.shared")
	})
	s.Step("devco:D-697/the-printed-line-run-from-home-changes-that-folder", func() {
		// the line the member was handed, run by the admin from their HOME
		w.admin.Lore(".", fromHome...).OK(t)
		// set at the folder the member meant, not the org or the store root
		w.admin.LoreIn(w.aapp, "policy", "show", "--id", "branches.shared").OK(t).Says(t, "feat/a", "folder "+place)
		w.admin.LoreIn(w.aapp, "branch", "mode", "--branch", "feat/a").OK(t).Says(t, "shared")
	})
	s.Step("devco:D-703/undo-commits-under-its-own-subject", func() {
		w.admin.LoreIn(w.aapp, "branch", "share", "--branch", "feat/a", "--undo", "--yes").OK(t).Says(t, "removed the share choice")
		if got := subject(); !strings.HasPrefix(got, "lore: branch share undo pol_") {
			t.Fatalf("share --undo commit subject = %q, want lore: branch share undo pol_…", got)
		}
		w.admin.LoreIn(w.aapp, "branch", "isolate", "--branch", "dev", "--yes").OK(t)
		if got := subject(); !strings.HasPrefix(got, "lore: branch isolate pol_") {
			t.Fatalf("isolate commit subject = %q", got)
		}
		w.admin.LoreIn(w.aapp, "branch", "isolate", "--branch", "dev", "--undo", "--yes").OK(t)
		if got := subject(); !strings.HasPrefix(got, "lore: branch isolate undo pol_") {
			t.Fatalf("isolate --undo commit subject = %q, want lore: branch isolate undo pol_…", got)
		}
	})
}
