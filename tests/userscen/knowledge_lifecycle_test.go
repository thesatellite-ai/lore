//go:build userscen

package userscen

// Knowledge lifecycle scenarios, one sub-test per kind (memory, rule,
// decision, hotfix, pattern, playbook, prompt, architecturenote,
// cookbookrecipe, incident, tastepref, behaviour, snapshot, workflow,
// suggestion) through the real binary:
//   - add with --body and with the positional text, show --json (envelope,
//     fields, creator), list --json (count, contains)
//   - edit changes only the given field; edit with no flag is refused
//   - delete asks first (E_NEEDS_CONFIRM, exit 26, row kept), --yes trashes
//     it; show then says E_IN_TRASH (exit 23) with the restore command; list
//     hides it, --include-deleted and --deleted-only show it; a second
//     delete is E_IN_TRASH too
//   - show of an unknown id: E_NOT_FOUND exit 27; a wrong-kind id or junk:
//     E_INVALID_ID exit 20, in text and in the --json error envelope
//   - archive / unarchive where the kind has them
//   - techdoc's own name/base-url lifecycle

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestKnowledgeLifecycleEveryKind(t *testing.T) {
	scenParallel(t)
	for _, k := range knowKinds {
		t.Run(k.noun, func(t *testing.T) {
			_, _, c := knowProject(t) // one project per kind: the kinds run in parallel
			knowNeighbour(t, c, k, "Neighbour", "a row of another kind")
			id := knowAdd(t, c, k, "First "+k.noun, "the body of "+k.noun)
			row := knowShowRow(t, c, k, id)
			if row.Body != "the body of "+k.noun {
				t.Fatalf("show body %q", row.Body)
			}
			if k.titleKey != "" && row.title(k) != "First "+k.noun {
				t.Fatalf("show title %q (key %s)", row.title(k), k.titleKey)
			}
			if k.creator && !strings.HasPrefix(row.CreatedBy, "act_") {
				t.Fatalf("show created_by_actor_id %q, want the person's act_ id", row.CreatedBy)
			}
			// The text form names the id (and the body where the kind prints it).
			txt := c.Lore(k.noun, "show", id).OK(t).Says(t, id)
			if k.textBody {
				txt.Says(t, "the body of "+k.noun)
			}
			if k.titleKey != "" { // INTEG-KNOWLEDGE-3: the title or name too
				txt.Says(t, "First "+k.noun)
			}

			// The positional text is the body, like --body.
			args := []string{k.noun, "add", "positional body " + k.noun, "--json"}
			if k.addTitle {
				args = append(args, "--title", "Second "+k.noun)
			}
			pos := th.DecodeJSON[knowAdded](t, c.Lore(args...)).ID
			if got := knowShowRow(t, c, k, pos).Body; got != "positional body "+k.noun {
				t.Fatalf("positional body stored as %q", got)
			}

			l := knowListRows(t, c, k)
			if !l.has(id) || !l.has(pos) || l.Count != 2 {
				t.Fatalf("list after two adds: %v", l.ids())
			}
			if want := strings.ToLower(l.Kind); !strings.HasSuffix(want, ".list") {
				t.Fatalf("list --json kind %q", l.Kind)
			}

			if k.canEdit {
				c.Lore(k.noun, "edit", id, "--body", "edited body").OK(t)
				got := knowShowRow(t, c, k, id)
				if got.Body != "edited body" {
					t.Fatalf("edit --body: body %q", got.Body)
				}
				if k.titleKey != "" && got.title(k) != "First "+k.noun {
					t.Fatalf("edit --body changed the title to %q", got.title(k))
				}
				if k.editFlag != "" {
					c.Lore(k.noun, "edit", id, k.editFlag, "Renamed "+k.noun).OK(t)
					got = knowShowRow(t, c, k, id)
					if got.title(k) != "Renamed "+k.noun || got.Body != "edited body" {
						t.Fatalf("edit %s: title %q body %q", k.editFlag, got.title(k), got.Body)
					}
				}
				c.Lore(k.noun, "edit", id).Fails(t, 20, "E_INVALID_INPUT")
				c.Lore(k.noun, "edit", knowUnknownID(id), "--body", "x").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
			}

			// delete asks first and keeps the row.
			c.Lore(k.noun, "delete", id).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "--yes")
			knowShowRow(t, c, k, id)
			c.Lore(k.noun, "delete", id, "--yes").OK(t).Says(t, "lore trash restore --id "+id)
			c.Lore(k.noun, "show", id).Fails(t, th.ExitRefused, "E_IN_TRASH", "lore trash restore --id "+id)
			if l := knowListRows(t, c, k); l.has(id) || !l.has(pos) {
				t.Fatalf("list after delete: %v", l.ids())
			}
			if l := knowListRows(t, c, k, "--include-deleted"); !l.has(id) || !l.has(pos) {
				t.Fatalf("list --include-deleted: %v", l.ids())
			}
			if l := knowListRows(t, c, k, "--deleted-only"); !l.has(id) || l.has(pos) {
				t.Fatalf("list --deleted-only: %v", l.ids())
			}
			c.Lore(k.noun, "delete", id, "--yes").Fails(t, th.ExitRefused, "E_IN_TRASH")

			// Unknown, wrong-kind and junk ids.
			c.Lore(k.noun, "show", knowUnknownID(pos)).Fails(t, th.ExitNotFound, "E_NOT_FOUND", knowUnknownID(pos))
			e := knowJSONErr(t, c.Lore(k.noun, "show", knowUnknownID(pos), "--json"))
			if e.Error.Code != "E_NOT_FOUND" {
				t.Fatalf("--json error code %q", e.Error.Code)
			}
			other := "tsk" + pos[len(k.prefix):]
			c.Lore(k.noun, "show", other).Fails(t, 20, "E_INVALID_ID", k.prefix+"_")
			c.Lore(k.noun, "show", "garbage").Fails(t, 20, "E_INVALID_ID")
			c.Lore(k.noun, "delete", "garbage", "--yes").Fails(t, 20, "E_INVALID_ID")
		})
	}
}

// Archive hides a row from list (and search) without trashing it; unarchive
// brings it back; --archived shows it.
func TestKnowledgeArchiveRoundTrip(t *testing.T) {
	scenParallel(t)
	for _, k := range knowKinds {
		if !k.archive {
			continue
		}
		t.Run(k.noun, func(t *testing.T) {
			_, _, c := knowProject(t) // one project per kind: the kinds run in parallel
			id := knowAdd(t, c, k, "Archivable "+k.noun, "zebracorn "+k.noun)
			c.Lore(k.noun, "archive", id).OK(t)
			if l := knowListRows(t, c, k); l.has(id) {
				t.Fatalf("archived row still listed: %v", l.ids())
			}
			if l := knowListRows(t, c, k, "--archived"); !l.has(id) {
				t.Fatalf("list --archived misses it: %v", l.ids())
			}
			knowShowRow(t, c, k, id) // show still finds it
			c.Lore("search", "zebracorn", "--tables", k.noun).OK(t).Never(t, id)
			c.Lore("search", "zebracorn", "--tables", k.noun, "--include-archived").OK(t).Says(t, id)
			c.Lore(k.noun, "unarchive", id).OK(t)
			if l := knowListRows(t, c, k); !l.has(id) {
				t.Fatalf("unarchived row not listed: %v", l.ids())
			}
			// Twice in a row is harmless and the row stays listed.
			c.Lore(k.noun, "unarchive", id)
			if l := knowListRows(t, c, k); !l.has(id) {
				t.Fatalf("second unarchive lost the row: %v", l.ids())
			}
		})
	}
}

// techdoc is a reference to external docs: --name and --base-url.
func TestKnowledgeTechdocLifecycle(t *testing.T) {
	_, _, c := knowProject(t)
	add := th.DecodeJSON[knowAdded](t, c.Lore("techdoc", "add", "--name", "Go stdlib", "--base-url", "https://pkg.go.dev/std", "--description", "the standard library", "--json"))
	if !strings.HasPrefix(add.ID, "tdc_") && !strings.Contains(add.ID, "_") {
		t.Fatalf("techdoc id %q", add.ID)
	}
	c.Lore("techdoc", "show", add.ID).OK(t).Says(t, "Go stdlib", "https://pkg.go.dev/std")
	type doc struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Desc    string `json:"description"`
	}
	type docs struct {
		Count int   `json:"count"`
		Data  []doc `json:"data"`
	}
	l := th.DecodeJSON[docs](t, c.Lore("techdoc", "list", "--json"))
	if l.Count != 1 || l.Data[0].ID != add.ID || l.Data[0].BaseURL != "https://pkg.go.dev/std" || l.Data[0].Desc != "the standard library" {
		t.Fatalf("techdoc list: %+v", l)
	}
	c.Lore("techdoc", "add").Fails(t, 20, "name")
	c.Lore("techdoc", "delete", add.ID).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM")
	c.Lore("techdoc", "delete", add.ID, "--yes").OK(t)
	if l := th.DecodeJSON[docs](t, c.Lore("techdoc", "list", "--json")); l.Count != 0 {
		t.Fatalf("techdoc list after delete: %+v", l)
	}
}
