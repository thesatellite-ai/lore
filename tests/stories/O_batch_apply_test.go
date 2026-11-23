//go:build stories && !windows

package stories

// O_batch_apply_test.go — devco:D-925 (owner 440-action import, item 5,
// bug hunt H-183) on the owner-shaped machine (story O): every `lore
// memory add|edit|archive` and `lore meta set` was its own store commit,
// so a few hundred actions took hours. `lore batch apply --file` runs a
// JSON-lines file of mixed writes through each command's own checks and
// lands them as ONE commit per store; --dry-run writes nothing; a bad line
// refuses the whole file; the printed undo command runs where printed.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/testtime"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// batchApplyOps is the size of the mixed file the speed step applies.
const batchApplyOps = 400

func TestStoryOBatchApplyOneCommitPerStore(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	kb := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-kb")
	orbit := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/orbit"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	dir := t.TempDir()
	var meClone, kbClone string
	var seeded []string
	var code *teamharness.Clone

	s.Step("D-925/seed-two-stores-in-one-apply", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		code = ada.CloneTo(orbit, ada.Code("orbit"))
		ada.LoreIn(code, "init").OK(t)
		ada.LoreIn(code, "store", "init", "--name", "kb", "--url", kb.Dir, "--yes").OK(t)
		ada.LoreIn(code, "memory", "add", "--body", "first note of me", "--to", "me/notes", "--create", "--yes").OK(t)
		ada.LoreIn(code, "memory", "add", "--body", "first note of kb", "--to", "kb/notes", "--create", "--yes").OK(t)
		ada.LoreIn(code, "meta", "add", "--name", "team", "--type", "text", "--to", "me/notes", "--yes").OK(t)
		meClone, kbClone = oStoreClone(ada, "_local", "lore-me"), oStoreClone(ada, "_local", "lore-kb") // a local bare remote clones under _local
		var lines []string
		for i := range 100 {
			lines = append(lines, oApplyLine("memory add", map[string]any{"to": "me/notes", "body": "seed note " + strconv.Itoa(i) + " about the billing service"}))
		}
		before := oCommits(t, meClone)
		ada.LoreIn(code, "batch", "apply", "--file", oWriteLines(t, dir, "seed.jsonl", lines), "--yes").OK(t).
			Says(t, "applied 100 line(s) in 1 store write(s) (stores: me/notes)")
		oCheck(t, "D-925/seed-one-commit", oCommits(t, meClone) == before+1, "100 adds made %d commits, want 1", oCommits(t, meClone)-before)
		seeded = oMemoryIDs(t, ada, code, "me/notes", "seed note ")
		oCheck(t, "D-925/seed-listed", len(seeded) == 100, "memory list shows %d seed notes, want 100", len(seeded))
	})

	s.Step("D-925/400-mixed-ops-one-commit-per-store-under-the-bar", func() {
		var lines []string
		for i := range batchApplyOps - len(seeded) {
			to := "me/notes"
			if i%2 == 1 {
				to = "kb/notes"
			}
			lines = append(lines, oApplyLine("memory add", map[string]any{"to": to, "body": "imported note " + strconv.Itoa(i)}))
		}
		for i, id := range seeded {
			switch i % 3 {
			case 0:
				lines = append(lines, oApplyLine("memory edit", map[string]any{"id": id, "body": "rewritten seed " + strconv.Itoa(i)}))
			case 1:
				lines = append(lines, oApplyLine("memory archive", map[string]any{"id": id}))
			default:
				lines = append(lines, oApplyLine("meta set", map[string]any{"id": id, "pair": "team=billing"}))
			}
		}
		file := oWriteLines(t, dir, "mixed.jsonl", lines)
		me0, kb0 := oCommits(t, meClone), oCommits(t, kbClone)
		start := time.Now()
		ada.LoreIn(code, "batch", "apply", "--file", file, "--yes").OK(t).
			Says(t, "applied "+strconv.Itoa(batchApplyOps)+" line(s) in 2 store write(s) (stores: me/notes, kb/notes)", "(undo: lore batch undo --id bat_")
		took := time.Since(start)
		oCheck(t, "D-925/commits-equal-stores", oCommits(t, meClone) == me0+1 && oCommits(t, kbClone) == kb0+1,
			"%d mixed ops made %d commits in me and %d in kb, want 1 each", batchApplyOps, oCommits(t, meClone)-me0, oCommits(t, kbClone)-kb0)
		oCheck(t, "D-925/speed", took <= testtime.SpeedBar(90*time.Second), "%d mixed ops took %s", batchApplyOps, took)
		ada.LoreIn(code, "memory", "show", "--id", seeded[0]).OK(t).Says(t, "rewritten seed 0")
		ada.LoreIn(code, "meta", "get", "--id", seeded[2]).OK(t).Says(t, "billing")
	})

	s.Step("D-925/dry-run-and-a-bad-line-write-nothing", func() {
		good := oApplyLine("memory add", map[string]any{"to": "me/notes", "body": "should never land"})
		before := oCommits(t, meClone)
		ada.LoreIn(code, "batch", "apply", "--file", oWriteLines(t, dir, "dry.jsonl", []string{good}), "--dry-run").OK(t).
			Says(t, "checked 1 line(s)", "nothing written (--dry-run)")
		bad := oApplyLine("memory edit", map[string]any{"id": seeded[0], "nope": "x"})
		ada.LoreIn(code, "batch", "apply", "--file", oWriteLines(t, dir, "bad.jsonl", []string{good, bad, `{"op": "memory delete", "input": {"id": "` + seeded[1] + `"}}`}), "--yes").
			Refused(t, "line 2:", "line 3: \"memory delete\" cannot run in a batch apply", "nothing written")
		oCheck(t, "D-925/nothing-written", oCommits(t, meClone) == before, "a dry run or refused file made %d commits", oCommits(t, meClone)-before)
		ada.LoreIn(code, "search", "--query", "should never land").OK(t).Never(t, "should never land.")
	})

	s.Step("D-925/the-printed-undo-runs-where-printed", func() {
		out := ada.LoreIn(code, "batch", "apply", "--file", oWriteLines(t, dir, "small.jsonl", []string{
			oApplyLine("memory add", map[string]any{"to": "me/notes", "body": "a note to take back"}),
		}), "--yes").OK(t).Out()
		undo := regexp.MustCompile(`undo: lore (batch undo --id bat_[0-9a-f]+)`).FindStringSubmatch(out)
		oCheck(t, "D-925/undo-printed", len(undo) == 2, "batch apply printed no undo command:\n%s", out)
		// devco:D-925: from the code repo the personal store's folder is
		// reached as a row id reaches it, and the writer's machine row the
		// batch made on its first write into the store is kept
		ada.LoreIn(code, append(oShellWords(undo[1]), "--yes")...).OK(t).
			Says(t, "moved 1 row batch", "to the trash").Never(t, "already deleted")
		ada.LoreIn(code, "search", "--query", "a note to take back").OK(t).Never(t, "a note to take back.")
	})
}

// oApplyLine is one batch apply line.
func oApplyLine(opName string, input map[string]any) string {
	b, err := json.Marshal(map[string]any{"op": opName, "input": input})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// oWriteLines writes lines as a JSON-lines file in dir and returns its path.
func oWriteLines(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// oCommits is how many commits HEAD of the git work tree dir has.
func oCommits(t *testing.T, dir string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-list in %s: %v", dir, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// oMemoryIDs is the ids of the memories in place whose body starts with prefix.
func oMemoryIDs(t *testing.T, m *OwnerMachine, c *teamharness.Clone, place, prefix string) []string {
	t.Helper()
	out := m.LoreIn(c, "memory", "list", "--to", place, "--limit", "1000", "--json").OK(t).Stdout
	var got struct {
		Data []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("memory list --json: %v\n%s", err, out)
	}
	var ids []string
	for _, r := range got.Data {
		if strings.HasPrefix(r.Body, prefix) {
			ids = append(ids, r.ID)
		}
	}
	return ids
}
