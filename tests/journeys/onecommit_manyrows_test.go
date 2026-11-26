//go:build journeys && !windows

package journeys

// onecommit_manyrows_test.go — a store whose history has one commit with
// many row files (bug hunt H-280, devco:D-1018). `lore store compact`
// leaves such a commit (its snapshot holds every row of the store), and
// so does a big import. The roster check read every row version of such
// a commit at once, its kept result wrote each path twice and every id as
// text, and doctor's reads of every folder stayed in memory for the whole
// command: a compacted store of 1.26M rows took doctor 45+ minutes. Now
// the check reads one commit's rows a window at a time and keeps one small
// record per row file. This journey checks the check still judges right
// across such a commit, on a machine that checks the whole history again
// (a new clone, a lore upgrade) and in the next command, which reads the
// kept result:
//
//   - Lee (admin) commits 600 rows by hand in one commit; Ann (member)
//     makes herself admin by hand;
//   - with the kept check gone, lore sync checks the whole history: Ann's
//     change is held back, every one of Lee's rows is read;
//   - the kept check holds one small record per row file, and the next
//     commands (member list, show, doctor) read it with the same verdicts.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestJourneyOneCommitOfManyRows(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	const rows = 600
	var seed, last string

	w.Step("day1/team-with-a-member", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "member", "add", "--email", tlAnn.Email, "--name", tlAnn.Name, "--handle", "ann", "--role", "member", "--yes").OK(t)
		out := tt.lore(tlLee, "memory", "add", "bulk seed note", "--to", tlOrg+"/"+tlProject, "--create").OK(t)
		seed = regexp.MustCompile(`mem_[0-9a-f]{32}`).FindString(out.Out())
		if seed == "" {
			t.Fatalf("no memory id in: %s", out.Out())
		}
		tt.lore(tlLee, "sync").OK(t)
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		tlFirstSteps(t, ann, dir, tlTeamSetup, "member", tlAnn)
		tlApply(t, ann, dir, tlTeamSetup, "join", map[string]any{"invite": tt.store.SSHURL()})
		tlLastSteps(t, ann, dir, tlTeamSetup, []string{tlFolders})
		tt.lore(tlAnn, "sync").OK(t)
	})

	w.Step("day1/one-commit-of-600-rows-and-a-hand-made-promotion", func() {
		hand := lee.Clone(tt.store, "bulk")
		files := strings.Fields(hand.Git("ls-files", "--", "*/"+seed+".json"))
		if len(files) != 1 {
			t.Fatalf("the seed row's file: %v", files)
		}
		tmpl, _ := hand.ReadFile(files[0])
		dir := filepath.Dir(files[0])
		for i := range rows {
			id := fmt.Sprintf("%s%012x", seed[:len(seed)-12], 0x300000+i) // same prefix and version bits, new tail
			body := strings.Replace(strings.ReplaceAll(tmpl, seed, id), "bulk seed note", fmt.Sprintf("bulk note %d", i), 1)
			hand.WriteFile(dir+"/"+id+".json", body)
			last = id
		}
		hand.Git("add", "-A")
		hand.Git("commit", "-q", "--no-verify", "-m", "import 600 notes")
		hand.Git("push", "-q", "origin", "HEAD")

		annHand := tt.m[tlAnn.Email].Clone(tt.store, "hand")
		annID := tt.roster(t, tlLee)["human:"+tlAnn.Email].ID
		stints := strings.Fields(annHand.Git("grep", "-l", annID, "--", "*actor_periods*"))
		if len(stints) != 1 {
			t.Fatalf("Ann's stint files: %v", stints)
		}
		stint, _ := annHand.ReadFile(stints[0])
		promoted := strings.Replace(stint, `"org_role": "member"`, `"org_role": "admin"`, 1)
		if promoted == stint {
			t.Fatalf("no member org role in Ann's stint: %s", stint)
		}
		annHand.Git("pull", "-q", "--no-rebase", "origin", "HEAD")
		annHand.WriteFile(stints[0], promoted)
		annHand.Git("commit", "-q", "--no-verify", "-am", "roster fix")
		annHand.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day1/a-whole-history-check-reads-it-right", func() {
		clones := lee.ClonesOfStore(tt.store)
		if len(clones) != 1 {
			t.Fatalf("Lee's clones of the store: %v", clones)
		}
		kept, _ := filepath.Glob(filepath.Join(clones[0], ".git", "lore-authority.gob*"))
		for _, f := range kept {
			if err := os.Remove(f); err != nil {
				t.Fatal(err)
			}
		}
		tt.lore(tlLee, "sync").OK(t).Says(t, "held back in", "by "+tlAnn.Email, "makes its author an admin and the author is not an admin")
		info, err := os.Stat(filepath.Join(clones[0], ".git", "lore-authority.gob"))
		if err != nil {
			t.Fatalf("the whole-history check kept no result: %v", err)
		}
		if per := info.Size() / rows; per > 200 {
			t.Errorf("the kept check takes %d bytes per row file (%d bytes, %d rows), want one small record each", per, info.Size(), rows)
		}
	})

	w.Step("day1/the-next-commands-read-the-kept-check", func() {
		tt.lore(tlLee, "member", "list").OK(t).Says(t, "@ann").Never(t, "admin   @ann")
		tt.lore(tlLee, "show", "--id", last).OK(t).Says(t, fmt.Sprintf("bulk note %d", rows-1))
		tt.lore(tlLee, "doctor").Says(t, "held back")
	})
}
