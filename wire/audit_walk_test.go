package wire_test

import (
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	opsrow "github.com/thesatellite-ai/lore/ops/row"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// showSuffix marks a noun's show operation.
const showSuffix = ".show"

// otherActor is an actor id no seeded row was written by.
const otherActor = "act_01936f0000007b1b8c2d3e4f5a6b7c8e"

// auditListSetups seed the rows the generic seeder cannot fill (a
// polymorphic target), as walkListSetups does for the trash walk.
var auditListSetups = map[string]func(h *opstest.H) (map[string]string, string){
	"comment.list": func(h *opstest.H) (map[string]string, string) {
		return map[string]string{"entity_table": opstest.Quote(string(schema.TableProjects)), "entity_id": opstest.Quote(h.Project)}, ""
	},
	// a person's stints: the list names the person
	"actor.period.list": func(*opstest.H) (map[string]string, string) {
		return map[string]string{}, `"actor":"` + opstest.Actor + `"`
	},
	"secrets.allow.list": func(h *opstest.H) (map[string]string, string) {
		return map[string]string{"row_id": opstest.Quote(h.Project)}, ""
	},
	// scope list answers for this checkout's repo identity
	"scope.list": func(h *opstest.H) (map[string]string, string) {
		return map[string]string{"repo_id": opstest.Quote(h.SeedRepoIdentity())}, ""
	},
	"link.list": func(h *opstest.H) (map[string]string, string) {
		return map[string]string{"entity_table": opstest.Quote(string(schema.TableProjects)), "entity_id": opstest.Quote(h.Project)}, ""
	},
}

// auditNoTable are the lists that list no synced table, so they have no
// writer filter; every other `<noun>.list` must reach a table (schema.Noun
// or the plural rule) and take --created-by/--updated-by.
var auditNoTable = map[string]string{
	"batch.list":          "lists delete batches, a view over the trash of every table",
	"config.list":         "lists ~/.lore/config.yaml settings, not rows",
	"machine.path.list":   "lists where lore keeps its own files on this machine, not rows",
	"secrets.list":        "lists the secret references found in fields of every table, not rows of one",
	"setup.list":          "lists this machine's setups (local files, not synced rows)",
	"setup.trash.list":    "lists this machine's removed setups (local files)",
	"store.folder.list":   "lists a store's folders (their _meta.json files), not rows",
	"store.list":          "lists the stores attached in .lore/stores.yaml, not rows",
	"sync.trash.list":     "lists sync's trash of every table; the writer filters of each noun list apply there",
	"task.checklist.list": "a checklist is a field of its task row, not rows of a table",
	"trash.list":          "lists the trash of every table, not one table",
}

// auditSeedSkip are lists whose rows the walk cannot seed in opstest's
// repo store; their flags are still checked, and engine.List filters them
// like every other table (TestAuditFilters in engine covers the filter).
var auditSeedSkip = map[string]string{}

// auditOrgLists list rows that live only in an org store (team profiles
// and invites, TP-7): the walk runs them in an opstest.WithOrg harness and
// seeds their rows into opstest.OrgRoster.
var auditOrgLists = map[string]bool{
	"contact_type.list": true, "role.list": true, "actor.contact.list": true,
	"actor.period.list": true, "project.member.list": true, "team.invite.list": true,
}

// TestAuditOnEveryList is INTEG-AUDIT-1's command-tree walk over wire's
// real registry: every noun list with a table offers --created-by and
// --updated-by (the shared metalist.Audit), a freshly seeded row is listed
// for its writer and left out for another actor, its list record names
// created_by/updated_by, and the noun's show --json carries the four
// audit fields. A new noun list cannot forget them. The walk must reach a
// minimum number of lists, so it can never pass by finding none.
func TestAuditOnEveryList(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	shows := map[string]bool{}
	for _, e := range h.App.Registry().Entries() {
		if noun, ok := strings.CutSuffix(e.Spec().Name, showSuffix); ok {
			shows[noun] = true
		}
	}
	checked := 0
	for _, e := range h.App.Registry().Entries() {
		name := e.Spec().Name
		noun, isList := strings.CutSuffix(name, listSuffix)
		if !isList {
			continue
		}
		table, ok := tableOfNoun(reg, noun)
		if !ok {
			if _, allowed := auditNoTable[name]; !allowed {
				t.Errorf("%s lists no table the walk can find: declare the table it lists (schema.Noun) or add it to auditNoTable with the reason", name)
			}
			continue
		}
		if !hasInput(e, "created_by") || !hasInput(e, "updated_by") {
			t.Errorf("%s lists %s but has no --created-by/--updated-by: embed metalist.Audit (or metalist.Flags)", name, table)
			continue
		}
		setup, hasSetup := walkListSetups[name]
		if s, ok := auditListSetups[name]; ok {
			setup, hasSetup = s, true
		}
		if needs := requiredInputs(e); len(needs) > 0 && !hasSetup {
			t.Errorf("%s needs %v: give it an entry in walkListSetups so the walk can run it", name, needs)
			continue
		}
		if why, skip := auditSeedSkip[name]; skip {
			t.Logf("%s: flags checked, rows not seeded: %s", name, why)
			continue
		}
		t.Run(name, func(t *testing.T) {
			h, src := opstest.New(t), store.SourceRepo // one store per list: a seeded project never makes another list ambiguous
			if auditOrgLists[name] {
				h, src = opstest.WithOrg(t), opstest.OrgRoster
			}
			set, base := map[string]string{}, ""
			if hasSetup {
				set, base = setup(h)
			}
			if _, given := set[fieldProjectID]; !given && hasField(reg, table, fieldProjectID) {
				set[fieldProjectID] = opstest.Quote(h.Project)
			}
			id := h.SeedTo(src, table, set)
			input := func(extra string) string { return walkInput(base, extra) }
			for _, c := range []struct {
				in   string
				want bool
			}{
				{`"created_by":"` + opstest.Actor + `"`, true},
				{`"updated_by":"` + opstest.Actor + `"`, true},
				{`"created_by":"` + otherActor + `"`, false},
				{`"updated_by":"` + otherActor + `"`, false},
			} {
				res, rerr := h.Run(name, input(c.in))
				if rerr != nil {
					t.Fatalf("%s %s: %v", name, c.in, rerr)
				}
				if got := strings.Contains(h.JSON(res), id); got != c.want {
					t.Fatalf("%s %s: lists %s = %v, want %v: %s", name, c.in, id, got, c.want, h.JSON(res))
				}
				if c.want && !strings.Contains(h.JSON(res), `"created_by":"`+opstest.Actor+`"`) {
					t.Fatalf("%s record has no created_by: %s", name, h.JSON(res))
				}
			}
			if noun == "machine" { // machine show takes the machine id, not the row id
				return
			}
			// a noun with no show of its own (comment, link, tag, ...) is
			// shown by `lore show <id>` (row.show), with the record at the
			// top level rather than under data
			showOp, path := noun+showSuffix, []string{"data"}
			if !shows[noun] {
				showOp, path = opsrow.OpShow, nil
			} else if se, ok := h.App.Registry().Lookup(showOp); ok && !hasInput(se, "id") {
				showOp, path = opsrow.OpShow, nil // its show takes a name (task.template.show, task.view.show)
			}
			show, serr := h.Run(showOp, `{"id":"`+id+`"}`)
			if serr != nil {
				t.Fatalf("%s: %v", showOp, serr)
			}
			for _, k := range []string{"created_at", "updated_at", "created_by", "updated_by"} {
				if !strings.Contains(h.JSON(show), `"`+k+`":`) {
					t.Fatalf("%s --json has no %s: %s", showOp, k, h.JSON(show))
				}
			}
			// INTEG-AUDIT-2: the plain show prints the one writers line
			if line := contenttest.WriterLine(t, h.JSON(show), path...); !strings.Contains(line, " by "+opstest.Actor) || !strings.Contains(opstest.Text(show), line) {
				t.Fatalf("%s text has no writers line %q: %q", showOp, line, opstest.Text(show))
			}
		})
		checked++
	}
	if checked < 20 {
		t.Fatalf("the walk reached %d noun lists; the registry has more", checked)
	}
}
