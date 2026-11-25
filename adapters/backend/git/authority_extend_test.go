package git

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestAuthorityExtendEqualsFullReplay is the equality property of the
// carried-on replay (devco:D-999, store growth item 5): over random
// histories (plain commits, merges onto the clone's HEAD, fast-forwards
// to a teammate's merge, merges with a version neither side had,
// rewrites, squashed unpushed commits, new processes) the replay carried on from where it stopped
// gives exactly what a full replay of the whole history gives: the same
// roster, holds, credits, refused commits, admins after every commit and
// org file changes. The commits change roster stints (a member making
// himself admin, the admin demoting himself), rows with a borrowed
// writer, project membership and lore-org.yaml, by the admin, a member
// and a contractor. It also proves the carried-on path is the one taken
// after a sync's merge (fewer commits replayed than the whole history).
func TestAuthorityExtendEqualsFullReplay(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 4} {
		t.Run(strconv.FormatUint(seed, 10), func(t *testing.T) { extendMatchesReplay(t, seed, 36) })
	}
}

// extendMatchesReplay runs steps random history changes in one clone and
// checks the replay after each against a full replay.
func extendMatchesReplay(t *testing.T, seed uint64, steps int) {
	o := newOrgTeam(t)
	b := orgFolderClone(t, o.h.remote)
	r := rand.New(rand.NewPCG(seed, seed*7919))
	people := []person{o.boss, o.max, o.cy}
	mems := []ids.AnyID{mustID[ids.Memory](t), mustID[ids.Memory](t), mustID[ids.Memory](t)}
	pm := mustID[ids.ProjectMember](t)
	orgFile := rowfs.FolderOrg + "/" + orgFileName
	tick := 0
	// change is one random file change: its path and body
	change := func() (string, string) {
		tick++
		wall := fmt.Sprintf("2026-02-01T00:%02d:%02d.000Z", tick/60, tick%60)
		switch r.IntN(6) {
		case 0:
			role := []string{"admin", "member"}[r.IntN(2)]
			return orgPath("actor_periods", o.max.period), o.max.periodRowAt(wall, role, "")
		case 1:
			return orgPath("actor_periods", o.boss.period), o.boss.periodRowAt(wall, []string{"admin", "member"}[r.IntN(2)], "")
		case 2:
			// the comment makes each version new; the stores repeat, so a
			// version can say what an accepted one said (a reformat)
			return orgFile, "# v" + strconv.Itoa(tick) + "\nname: acme\nstores:\n  - url: git@example.invalid:acme/s" + strconv.Itoa(r.IntN(4)) + ".git\n"
		case 3:
			who := people[r.IntN(len(people))]
			return orgPath("project_members", pm), rowJSON("project_members", pm, map[string]string{"actor_id": `"` + who.actor.String() + `"`, "project_id": `"` + o.project.String() + `"`, "note": `"v` + strconv.Itoa(tick) + `"`})
		default:
			id := mems[r.IntN(len(mems))]
			writer := people[r.IntN(len(people))]
			return orgPath("memories", id), stampedRow(id, writer.actor, wall, "v"+strconv.Itoa(tick))
		}
	}
	commit := func() {
		p, body := change()
		commitIn(t, b.work, people[r.IntN(len(people))].email, p, body)
	}
	// back is a commit k first parents below HEAD (HEAD when there is none)
	back := func() string {
		for k := 1 + r.IntN(3); k > 0; k-- {
			if out, err := tryGit(t, b.work, "rev-parse", "--verify", "--quiet", "HEAD~"+strconv.Itoa(k)); err == nil {
				return trimmed([]byte(out))
			}
		}
		return "HEAD"
	}
	merge := func(author string, args ...string) {
		gitT(t, b.work, append([]string{"-c", "user.name=x", "-c", "user.email=" + author, "merge", "--quiet", "--no-edit"}, args...)...)
	}
	commit()
	authorityNow(t, b)
	carried := 0
	for step := range steps {
		side := "side" + strconv.Itoa(step)
		kind := r.IntN(11)
		switch kind {
		case 5, 6: // a merge onto HEAD, as a sync makes it (maybe with a version neither side had)
			gitT(t, b.work, "checkout", "--quiet", "-b", side, back())
			for range 1 + r.IntN(2) {
				commit()
			}
			gitT(t, b.work, "checkout", "--quiet", testBranch)
			author := people[r.IntN(len(people))].email
			if kind == 6 {
				merge(author, "--no-ff", "--no-commit", "-X", "theirs", side)
				commit()
			} else {
				merge(author, "--no-ff", "-X", "theirs", side)
			}
		case 7: // a fast-forward to a teammate's merge of HEAD into their line
			gitT(t, b.work, "checkout", "--quiet", "-b", side, back())
			commit()
			merge(people[r.IntN(len(people))].email, "--no-ff", "-X", "ours", testBranch)
			gitT(t, b.work, "checkout", "--quiet", testBranch)
			merge(o.boss.email, "--ff-only", side)
		case 8: // a rewrite (a force-pushed history)
			gitT(t, b.work, "reset", "--quiet", "--hard", back())
			commit()
		case 10: // a squash: the unpushed commits on top become one (push squashing, H-255)
			for range 1 + r.IntN(2) {
				commit()
			}
			gitT(t, b.work, "reset", "--quiet", "--soft", back())
			gitT(t, b.work, "-c", "user.name=x", "-c", "user.email="+o.boss.email, "commit", "--quiet", "--no-verify", "--allow-empty", "-m", "squashed")
		case 9: // a new process, then a commit
			forgetAuthority(b)
			commit()
		default:
			commit()
		}
		before := replayCount()
		got := authorityNow(t, b)
		n := replayCount() - before
		all, err := b.replayCommits(context.Background(), got.head)
		if err != nil {
			t.Fatal(err)
		}
		if (kind == 5 || kind == 6) && n < int64(len(all)) {
			carried++
		}
		want := fullReplay(t, b)
		sameAuthority(t, got, want)
		if t.Failed() {
			t.Fatalf("step %d (kind %d, seed %d): the carried-on replay differs from a full replay", step, kind, seed)
		}
	}
	if carried == 0 {
		t.Errorf("seed %d: no merge onto HEAD was carried on (every one replayed the whole history)", seed)
	}
}
