package task_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTemplateStoresAndEdit (OV15c, RQ10, PC-TASK-2): a template found in
// one attached store only (the org store, from a repo) is used; template
// add refuses a name any attached store already holds, naming the id and
// the store; the same name reaching two stores through a sync (a row
// written straight through the engine, as a sync would) makes the
// resolver refuse naming both ids, and the ttp_ id picks one; template
// edit replaces the YAML so the next add uses the new form.
func TestTemplateStoresAndEdit(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false), withOrg(t))
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	orgFile := write("org.yaml", "task: {title: \"Org form\"}\n")
	repoFile := write("repo.yaml", "task: {title: \"Repo form\"}\n")

	orgTpl := h.ID(h.Must(opstask.OpTemplateAdd, `{"name":"standup","from_file":`+opstest.Quote(orgFile)+`,"to":"`+orgStore+`"}`))
	if out := opstest.Text(h.Must(opstask.OpAdd, `{"template":"standup"}`)); !strings.Contains(out, "Org form") {
		t.Errorf("org-only template from the repo: %s", out)
	}

	// PC-TASK-2: add into the repo refuses the name the org store holds,
	// and so does add into the org store again.
	for _, to := range []string{"", `,"to":"` + orgStore + `"`} {
		msg := h.Fails(opstask.OpTemplateAdd, `{"name":"standup","from_file":`+opstest.Quote(repoFile)+to+`}`, errs.Template)
		if !strings.Contains(msg, "already exists") || !strings.Contains(msg, orgTpl) || !strings.Contains(msg, "in "+orgStore) {
			t.Errorf("add%s of a name the org holds: %s", to, msg)
		}
	}
	if out := opstest.Text(h.Must(opstask.OpAdd, `{"template":"standup"}`)); !strings.Contains(out, "Org form") {
		t.Errorf("a refused add changed what the name resolves to: %s", out)
	}
	// The other way round: a name the repo holds is refused --to the org.
	retro := h.ID(h.Must(opstask.OpTemplateAdd, `{"name":"retro","from_file":`+opstest.Quote(repoFile)+`}`))
	if msg := h.Fails(opstask.OpTemplateAdd, `{"name":"retro","from_file":`+opstest.Quote(orgFile)+`,"to":"`+orgStore+`"}`, errs.Template); !strings.Contains(msg, retro+" in repo") {
		t.Errorf("add --to %s of a name the repo holds: %s", orgStore, msg)
	}

	// The same name arrives in the repo through a sync: the resolver
	// refuses and names both ids with their stores.
	repoTpl := h.Seed(schema.TableTaskTemplates, map[string]string{
		schema.FieldTemplateName: `"standup"`, schema.FieldTemplateTask: `{"title":"Repo form"}`})
	msg := h.Fails(opstask.OpAdd, `{"template":"standup"}`, errs.AmbiguousID)
	for _, want := range []string{orgTpl + " (" + orgStore + ")", repoTpl + " (repo)"} {
		if !strings.Contains(msg, want) {
			t.Errorf("two-store refusal does not name %q: %s", want, msg)
		}
	}
	if out := opstest.Text(h.Must(opstask.OpAdd, `{"template":"`+repoTpl+`"}`)); !strings.Contains(out, "Repo form") {
		t.Errorf("by id: %s", out)
	}
	if out := opstest.Text(h.Must(opstask.OpAdd, `{"template":"`+orgTpl+`"}`)); !strings.Contains(out, "Org form") {
		t.Errorf("org by id: %s", out)
	}

	// Edit replaces the form (by id: the name is ambiguous here).
	edited := write("edited.yaml", "task: {title: \"Edited form\", children: [{title: step}]}\n")
	h.Must(opstask.OpTemplateEdit, `{"name":"`+repoTpl+`","from_file":`+opstest.Quote(edited)+`}`)
	out := opstest.Text(h.Must(opstask.OpAdd, `{"template":"`+repoTpl+`"}`))
	if !strings.Contains(out, "Edited form") || strings.Contains(out, "Repo form") {
		t.Errorf("after edit: %s", out)
	}
	// A broken edit is refused and keeps the old form.
	bad := write("bad.yaml", "task: {title: a, colour: red}\n")
	h.Fails(opstask.OpTemplateEdit, `{"name":"`+repoTpl+`","from_file":`+opstest.Quote(bad)+`}`, errs.Template)
	if out := opstest.Text(h.Must(opstask.OpAdd, `{"template":"`+repoTpl+`"}`)); !strings.Contains(out, "Edited form") {
		t.Errorf("a refused edit changed the form: %s", out)
	}
}
