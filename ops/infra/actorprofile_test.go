package infra_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// internalRepo makes the harness repo internal: team profile rows are
// refused in a client-class store (TS-3), and a no-org repo's own store
// stands in for the org roster here (reads only).
func internalRepo(o *wire.Options) {
	o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
}

// seedPeople writes Mira (active, full profile), Carol (left), Dan
// (archived) and Eve (on_leave) and returns Mira's and Eve's ids.
func seedPeople(h *opstest.H) (string, string) {
	if h.Project == "" {
		h.SeedProject()
	}
	mira := h.Seed(schema.TableActors, map[string]string{"kind": `"human"`, "display_name": `"Mira Patel"`, "handle": `"mira"`,
		"stable_key": `"human:mira@acme.com"`, "title": `"Backend engineer"`, "timezone": `"Europe/Istanbul"`, "status": `"active"`})
	h.Seed(schema.TableActors, map[string]string{"kind": `"human"`, "display_name": `"Carol"`, "handle": `"carol"`, "active": `false`,
		"left_at": `"2026-05-31T10:00:00Z"`})
	h.Seed(schema.TableActors, map[string]string{"kind": `"human"`, "display_name": `"Dan"`, "status": `"archived"`, "active": `false`})
	eve := h.Seed(schema.TableActors, map[string]string{"kind": `"human"`, "display_name": `"Eve"`, "status": `"on_leave"`,
		"leave_from": `"2026-06-01"`, "leave_until": `"2026-06-30"`})
	prj := h.Project
	h.Seed(schema.TableActorContacts, map[string]string{"actor_id": `"` + mira + `"`, "type": `"github"`, "value": `"mirap"`, "value_norm": `"mirap"`, "identity": `true`})
	h.Seed(schema.TableActorContacts, map[string]string{"actor_id": `"` + mira + `"`, "type": `"email"`, "value": `"mira@acme.com"`, "value_norm": `"mira@acme.com"`, "primary": `true`, "label": `"work"`})
	h.Seed(schema.TableActorPeriods, map[string]string{"actor_id": `"` + mira + `"`, "type": `"employee"`, "started_on": `"2026-01-12"`, "org_role": `"Senior engineer"`})
	h.Seed(schema.TableProjectMembers, map[string]string{"actor_id": `"` + mira + `"`, "project_id": `"` + prj + `"`, "roles": `["dev","reviewer"]`, "started_on": `"2026-01-12"`})
	h.Seed(schema.TableProjectMembers, map[string]string{"actor_id": `"` + mira + `"`, "project_id": `"` + prj + `"`, "roles": `["qa"]`,
		"started_on": `"2025-01-01"`, "ended_on": `"2025-06-30"`, "end_reason": `"removed"`})
	return mira, eve
}

// TestActorShowProfile: show takes an id, @handle or stable key and adds
// status, profile, contacts, periods and memberships (open first) while
// keeping the v0.1.17 keys.
func TestActorShowProfile(t *testing.T) {
	h := opstest.New(t, internalRepo)
	mira, eve := seedPeople(h)
	for _, ref := range []string{mira, "@mira", "human:mira@acme.com"} {
		raw := h.JSON(h.Must(infra.OpActorShow, `{"id":"`+ref+`"}`))
		for _, want := range []string{`"id":"` + mira + `"`, `"kind":"human"`, `"display_name":"Mira Patel"`, `"active":true`, `"status":"active"`,
			`"title":"Backend engineer"`, `"timezone":"Europe/Istanbul"`, `"value":"mira@acme.com"`, `"value":"mirap"`, `"org_role":"Senior engineer"`,
			`"roles":["dev","reviewer"]`, `"project":"demo"`} {
			if !strings.Contains(raw, want) {
				t.Errorf("show %s misses %s:\n%s", ref, want, raw)
			}
		}
		if strings.Contains(raw, `"started_on":"2026-01-12","ended_on"`) || strings.Index(raw, `["dev","reviewer"]`) > strings.Index(raw, `["qa"]`) {
			t.Errorf("open membership must come first: %s", raw)
		}
	}
	txt := opstest.Text(h.Must(infra.OpActorShow, `{"id":"@mira"}`))
	for _, want := range []string{"title:", "contacts:    email", "periods:     2026-01-12 → now  employee  Senior engineer", "projects:    demo  dev, reviewer  since 2026-01-12", "2025-01-01 → 2025-06-30"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text misses %q:\n%s", want, txt)
		}
	}
	// Carol left (v0.1.17 deactivate): status left, the read-time period
	// is closed (left_at here predates the row, so it ends on its start:
	// never before it, teamprofile.PeriodsOf).
	raw := h.JSON(h.Must(infra.OpActorShow, `{"id":"@carol"}`))
	if !strings.Contains(raw, `"status":"left"`) || !strings.Contains(raw, `"ended_on":"`) || !strings.Contains(raw, `"source":"migrated"`) {
		t.Errorf("carol: %s", raw)
	}
	// Eve's leave ended before today: still on_leave, the text says so.
	if txt := opstest.Text(h.Must(infra.OpActorShow, `{"id":"`+eve+`"}`)); !strings.Contains(txt, "leave ended 2026-06-30; still on_leave") {
		t.Errorf("eve: %s", txt)
	}
	h.Fails(infra.OpActorShow, `{"id":"@nobody"}`, errs.NotFound)
}

// TestActorShowDanglingProject: a membership whose project row no store
// here holds (AllowDangling, TEAM_PROFILES 2.6) shows the prj_ id, while a
// resolvable one shows its name.
func TestActorShowDanglingProject(t *testing.T) {
	h := opstest.New(t, internalRepo)
	mira, _ := seedPeople(h)
	const far = "prj_01890a5d8a2b7c3d9e0f1a2b3c4d5e6f"
	h.Seed(schema.TableProjectMembers, map[string]string{"actor_id": `"` + mira + `"`, "project_id": `"` + far + `"`, "roles": `["ops"]`, "started_on": `"2026-02-01"`})
	txt := opstest.Text(h.Must(infra.OpActorShow, `{"id":"@mira"}`))
	for _, want := range []string{"demo  dev, reviewer", far + "  ops"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text misses %q:\n%s", want, txt)
		}
	}
}

// TestActorListStatus (TP-15): archived people are out of the default
// list and in with --archived; --status filters; left and on_leave are
// marked in text; a bad status is refused.
func TestActorListStatus(t *testing.T) {
	h := opstest.New(t, internalRepo)
	seedPeople(h)
	def := opstest.Text(h.Must(infra.OpActorList, `{}`))
	if strings.Contains(def, "Dan") || !strings.Contains(def, "Carol  (left)") || !strings.Contains(def, "Eve  (on_leave until 2026-06-30)") || !strings.Contains(def, "Mira Patel\n") {
		t.Errorf("default list:\n%s", def)
	}
	if all := opstest.Text(h.Must(infra.OpActorList, `{"archived":true}`)); !strings.Contains(all, "Dan  (archived)") {
		t.Errorf("--archived:\n%s", all)
	}
	for status, want := range map[string]string{"archived": "Dan", "left": "Carol", "on_leave": "Eve"} {
		got := opstest.Text(h.Must(infra.OpActorList, `{"status":"`+status+`"}`))
		if strings.Count(got, "\n") != 1 || !strings.Contains(got, want) {
			t.Errorf("--status %s:\n%s", status, got)
		}
	}
	h.Fails(infra.OpActorList, `{"status":"gone"}`, errs.InvalidInput)
}

// TestActorStatusNoteShown (TEAM_PROFILES 3.4): show and list print the
// status note (text and --json); a row written before status_note existed
// reads fine and carries no key.
func TestActorStatusNoteShown(t *testing.T) {
	h := opstest.New(t, internalRepo)
	_, eve := seedPeople(h)
	fay := h.Seed(schema.TableActors, map[string]string{"kind": `"human"`, "display_name": `"Fay"`, "status": `"on_leave"`,
		"leave_from": `"2026-06-01"`, "leave_until": `"2026-12-31"`, "status_note": `"parental leave"`})
	gus := h.Seed(schema.TableActors, map[string]string{"kind": `"human"`, "display_name": `"Gus"`, "status": `"left"`, "active": `false`,
		"status_note": `"moved abroad"`})
	list := opstest.Text(h.Must(infra.OpActorList, `{}`))
	for _, want := range []string{"Fay  (on_leave until 2026-12-31: parental leave)\n", "Gus  (left: moved abroad)\n", "Eve  (on_leave until 2026-06-30)\n"} {
		if !strings.Contains(list, want) {
			t.Errorf("list misses %q:\n%s", want, list)
		}
	}
	if raw := h.JSON(h.Must(infra.OpActorList, `{}`)); strings.Count(raw, `"status_note":`) != 2 || !strings.Contains(raw, `"status_note":"parental leave"`) {
		t.Errorf("list --json:\n%s", raw)
	}
	if txt := opstest.Text(h.Must(infra.OpActorShow, `{"id":"`+fay+`"}`)); !strings.Contains(txt, "  leave:       2026-06-01 → 2026-12-31\n  note:        parental leave\n") {
		t.Errorf("show fay:\n%s", txt)
	}
	if raw := h.JSON(h.Must(infra.OpActorShow, `{"id":"`+gus+`"}`)); !strings.Contains(raw, `"status_note":"moved abroad"`) {
		t.Errorf("show gus --json: %s", raw)
	}
	if raw := h.JSON(h.Must(infra.OpActorShow, `{"id":"`+eve+`"}`)); strings.Contains(raw, "status_note") {
		t.Errorf("an old row shows a status note: %s", raw)
	}
	if txt := opstest.Text(h.Must(infra.OpActorShow, `{"id":"`+eve+`"}`)); strings.Contains(txt, "note:") {
		t.Errorf("an old row shows a note line:\n%s", txt)
	}
}
