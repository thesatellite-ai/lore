//go:build journeys && !windows

package journeys

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// mcTableLine is one per-table line of a migrate run ("  memories: 2 rows
// (…)"); the fold's own line ("  tags: folded …") is not one.
var mcTableLine = regexp.MustCompile(`(?m)^  ([a-z_]+): (\d+ rows?) \(`)

// mcTagDeploy and mcTagInfra are the planted 0.1.17 tags.
const (
	mcTagDeploy = "tag_0e45506e64357c94a87e70ef1c6b0d01"
	mcTagInfra  = "tag_0e45506e64357c94a87e70ef1c6b0d02"
)

// mcCount is the count of a run's headline ("Would upgrade N rows" or
// "Upgraded N rows").
var mcCount = regexp.MustCompile(`(?m)^(?:Would upgrade|Upgraded) (\d+) rows?$`)

// mcTables lists a run's per-table lines as "table: N rows".
func mcTables(out string) []string {
	var got []string
	for _, m := range mcTableLine.FindAllStringSubmatch(out, -1) {
		got = append(got, m[1]+": "+m[2])
	}
	return got
}

// TestJourneyMigrateCountMatchesDryRun (E-2, E-3): notes lore 0.1.17
// tagged (its entity_tags links) are migrated by the new lore: the dry
// run and the run give the same count and the same per-table lines, the
// tag fold is one summary line (no line per row without --verbose), and
// right after the run doctor has no actor_roster warning (the run wrote
// the migrator's actor row with the rows it stamped).
func TestJourneyMigrateCountMatchesDryRun(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ana := w.Persona("Ana Tagger", "ana@example.test")
	repo := w.Remote("github.com/acme/tagged")
	laptop := UseOldLore(t, ana.Laptop())
	lap := laptop.Clone(repo, "app")

	w.Step("day1/old-lore-tagged-notes", func() {
		laptop.Old(nil, "identity", "set", "Ana Tagger", "--key", "human:ana@example.test").OK(t)
		laptop.Old(lap, "init", "--name", "app").OK(t)
		m1 := upAddMemory(t, laptop.Old(lap, "memory", "add", "--body", "deploys go through the release train"))
		m2 := upAddMemory(t, laptop.Old(lap, "memory", "add", "--body", "infra changes need a second reviewer"))
		meta, _ := lap.ReadFile(".lore/data/_meta.json")
		prj := regexp.MustCompile(`prj_[0-9a-f]{32}`).FindString(meta)
		if prj == "" {
			t.Fatalf("0.1.17's _meta.json names no project:\n%s", meta)
		}
		// lore 0.1.17's tags and their links as it kept them in .lore/data
		// (a tags row each, one entity_tags row per tagged row)
		const stamp = `"created_at":"2026-01-01T09:00:00Z","updated_at":"2026-01-01T09:00:00Z"`
		for id, name := range map[string]string{mcTagDeploy: "deploy", mcTagInfra: "infra"} {
			lap.WriteFile(".lore/data/tags/"+id+".json", `{"_table":"tags","_v":1,`+stamp+`,"id":"`+id+`","name":"`+name+`","project_id":"`+prj+`"}`)
		}
		for i, l := range [][2]string{{m1, mcTagDeploy}, {m1, mcTagInfra}, {m2, mcTagInfra}} {
			id := "etg_0e45506e64357c94a87e70ef1c6b0e0" + string(rune('1'+i))
			lap.WriteFile(".lore/data/entity_tags/"+id+".json", `{"_table":"entity_tags","_v":1,`+stamp+`,"entity_id":"`+l[0]+`","entity_table":"memories","id":"`+id+`","tag_id":"`+l[1]+`"}`)
		}
		upCommitPush(t, lap, "lore data")
	})

	desk := ana.NewLaptop("desk")
	dsk := desk.Clone(repo, "app")

	w.Step("month1/dry-run-and-run-agree", func() {
		dry := desk.LoreIn(dsk, "migrate", "run", "--dry-run").OK(t)
		run := desk.LoreIn(dsk, "migrate", "run").OK(t)
		dc, rc := mcCount.FindStringSubmatch(dry.Stdout), mcCount.FindStringSubmatch(run.Stdout)
		if dc == nil || rc == nil || dc[1] != rc[1] {
			t.Fatalf("headline: dry run %q, run %q; want the same count\n--- dry run\n%s\n--- run\n%s", dc, rc, dry.Stdout, run.Stdout)
		}
		if dt, rt := mcTables(dry.Stdout), mcTables(run.Stdout); len(dt) == 0 || !slices.Equal(dt, rt) {
			t.Fatalf("per-table lines: dry run %v, run %v", dt, rt)
		}
		run.Says(t, "tags: folded 3 entity_tags links into the tag_ids of 2 rows (memories 2)")
		for _, out := range []string{dry.Stdout, run.Stdout} {
			if strings.Contains(out, ".tag_ids = ") {
				t.Errorf("a run without --verbose lists the fold per row:\n%s", out)
			}
		}
	})

	w.Step("month1/doctor-after-migrate", func() {
		desk.LoreIn(dsk, "doctor").Never(t, "actor_roster")
	})
}
