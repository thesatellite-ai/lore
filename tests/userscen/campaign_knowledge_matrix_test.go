//go:build userscen

package userscen

// Knowledge campaign: the CRUD shape every knowledge kind shares, driven as
// one matrix over kinds × verbs × input classes through the real binary:
//   - odd values (10k chars, RTL, emoji, reserved words, SQL, slashes and
//     spaces, an id look-alike) round-trip through add, show, edit, list
//     and search;
//   - bad ids on every id-taking verb answer the exact code and exit, a
//     hint naming the next step, the same in the --json envelope, and write
//     nothing;
//   - delete without --yes asks (E_NEEDS_CONFIRM, the question names the
//     row, no internal package prefix) and writes nothing; the reverse path
//     delete → trash restore → purge with list after each step;
//   - archive / unarchive run twice.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestCampaignKnowledge_OddValuesEveryKind(t *testing.T) {
	scenParallel(t)
	for _, k := range campKnowKinds() {
		t.Run(k.noun, func(t *testing.T) {
			_, _, c := knowProject(t)
			for _, v := range campOddValues {
				t.Run(v.name, func(t *testing.T) {
					id := campMustAdd(t, c, k, v.in, v.in)
					row := knowShowRow(t, c, k, id)
					if row.Body != v.want {
						t.Fatalf("add stored body %q, want %q", knowCut(row.Body), knowCut(v.want))
					}
					if k.titleKey != "" && row.title(k) != v.want {
						t.Fatalf("add stored title %q, want %q", knowCut(row.title(k)), knowCut(v.want))
					}
					if !knowListRows(t, c, k).has(id) {
						t.Fatalf("list does not hold %s", id)
					}
					if k.canEdit {
						c.Lore(k.noun, "edit", id, "--body=edited "+v.in).OK(t)
						if got := knowShowRow(t, c, k, id).Body; got != "edited "+v.want {
							t.Fatalf("edit stored body %q", knowCut(got))
						}
						if k.editFlag != "" {
							c.Lore(k.noun, "edit", id, k.editFlag+"=renamed "+v.in).OK(t)
							if got := knowShowRow(t, c, k, id).title(k); got != "renamed "+v.want {
								t.Fatalf("edit %s stored %q", k.editFlag, knowCut(got))
							}
						}
					}
				})
			}
			// search finds a word of each searchable class
			for _, q := range []string{"שלום", "handlers", "lorem"} {
				r := c.Lore(k.noun, "search", q, "--json").OK(t)
				if !strings.Contains(r.Stdout, k.prefix+"_") {
					t.Fatalf("%s search %q found nothing:\n%s", k.noun, q, r)
				}
			}
		})
	}
}

// campBadID is one wrong id class.
type campBadID struct {
	name string
	code string
	exit int
}

func TestCampaignKnowledge_BadIDsEveryVerb(t *testing.T) {
	scenParallel(t)
	for _, k := range campKnowKinds() {
		t.Run(k.noun, func(t *testing.T) {
			_, _, c := knowProject(t)
			real := campMustAdd(t, c, k, "real", "a real row")
			other, _ := knowNeighbour(t, c, k, "other", "a row of another kind")
			verbs := [][]string{{"show"}, {"delete", "--yes"}}
			if k.canEdit {
				verbs = append(verbs, []string{"edit", "--body=x"})
			}
			if k.archive {
				verbs = append(verbs, []string{"archive"}, []string{"unarchive"})
			}
			ids := map[string]campBadID{
				knowUnknownID(real): {"unknown", "E_NOT_FOUND", th.ExitNotFound},
				other:               {"wrong-kind", "E_INVALID_ID", 20},
				"not-an-id":         {"junk", "E_INVALID_ID", 20},
			}
			before := campTreeHash(t, c.Dir)
			for _, vb := range verbs {
				for id, want := range ids {
					t.Run(vb[0]+"/"+want.name, func(t *testing.T) {
						args := append([]string{k.noun, vb[0], id}, vb[1:]...)
						r := c.Lore(args...).Fails(t, want.exit, want.code)
						if !strings.Contains(r.Stderr, "hint:") {
							t.Fatalf("no hint naming the next step:\n%s", r)
						}
						e := knowJSONErr(t, c.Lore(append(args, "--json")...))
						if e.Error.Code != want.code || e.Error.Hint == "" {
							t.Fatalf("--json error %+v, want code %s with a hint", e.Error, want.code)
						}
					})
				}
			}
			if after := campTreeHash(t, c.Dir); after != before {
				t.Fatalf("refused commands changed the repo")
			}
		})
	}
}

func TestCampaignKnowledge_DeleteRestorePurgeEveryKind(t *testing.T) {
	scenParallel(t)
	for _, k := range campKnowKinds() {
		t.Run(k.noun, func(t *testing.T) {
			_, _, c := knowProject(t)
			id := campMustAdd(t, c, k, "Doomed", "doomed body")
			keep := campMustAdd(t, c, k, "Kept", "kept body")

			// asking first: no terminal, no --yes
			before := campTreeHash(t, c.Dir)
			r := c.Lore(k.noun, "delete", id).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", id)
			if strings.Contains(r.Stderr, "engine:") {
				t.Fatalf("the question carries an internal package prefix:\n%s", r)
			}
			e := knowJSONErr(t, c.Lore(k.noun, "delete", id, "--json"))
			if strings.Contains(e.Error.Message, "engine:") || !strings.Contains(e.Error.Message, id) {
				t.Fatalf("--json question %q: want the row id and no internal prefix", e.Error.Message)
			}
			if campTreeHash(t, c.Dir) != before {
				t.Fatalf("an unconfirmed delete wrote to the repo")
			}

			c.Lore(k.noun, "delete", id, "--yes").OK(t)
			if l := knowListRows(t, c, k); l.has(id) || !l.has(keep) {
				t.Fatalf("list after delete: %v", l.ids())
			}
			if !knowListRows(t, c, k, "--deleted-only").has(id) {
				t.Fatalf("--deleted-only does not show the trashed row")
			}
			c.Lore(k.noun, "delete", id, "--yes").Fails(t, 23, "E_IN_TRASH")

			c.Lore("trash", "restore", id, "--yes").OK(t)
			if got := knowShowRow(t, c, k, id).Body; got != "doomed body" {
				t.Fatalf("restored body %q", got)
			}
			if l := knowListRows(t, c, k); !l.has(id) || !l.has(keep) {
				t.Fatalf("list after restore: %v", l.ids())
			}

			c.Lore(k.noun, "delete", id, "--purge", "--confirm", "--yes").OK(t)
			c.Lore(k.noun, "show", id).Fails(t, 23, "E_PURGED") // class refused (docs/reference/errors.md)
			if l := knowListRows(t, c, k, "--include-deleted"); l.has(id) || !l.has(keep) {
				t.Fatalf("list --include-deleted after purge: %v", l.ids())
			}
			c.Lore("trash", "restore", id, "--yes").Refused(t)
		})
	}
}

func TestCampaignKnowledge_ArchiveTwice(t *testing.T) {
	scenParallel(t)
	for _, k := range campKnowKinds() {
		if !k.archive {
			continue
		}
		t.Run(k.noun, func(t *testing.T) {
			_, _, c := knowProject(t)
			id := campMustAdd(t, c, k, "Old", "old body")
			for i := 0; i < 2; i++ {
				c.Lore(k.noun, "archive", id).OK(t)
				if knowListRows(t, c, k).has(id) {
					t.Fatalf("archive #%d: still in list", i+1)
				}
			}
			for i := 0; i < 2; i++ {
				c.Lore(k.noun, "unarchive", id).OK(t)
				if !knowListRows(t, c, k).has(id) {
					t.Fatalf("unarchive #%d: not in list", i+1)
				}
			}
		})
	}
}

func TestCampaignKnowledge_BulkDeleteDryRun(t *testing.T) {
	scenParallel(t)
	for _, noun := range []string{"memory", "rule"} {
		t.Run(noun, func(t *testing.T) {
			_, _, c := knowProject(t)
			k := knowKindByNoun(t, noun)
			a := campMustAdd(t, c, k, "", "alpha one")
			b := campMustAdd(t, c, k, "", "beta two")
			before := campTreeHash(t, c.Dir)
			r := c.Lore(noun, "delete", "--where", `body="alpha one"`, "--dry-run").OK(t).Says(t, a)
			if strings.Contains(r.Stdout, b) {
				t.Fatalf("dry run names a row the filter does not match:\n%s", r)
			}
			if campTreeHash(t, c.Dir) != before {
				t.Fatalf("--dry-run wrote to the repo")
			}
			c.Lore(noun, "delete", "a", "--dry-run").Fails(t, 20, "E_INVALID_INPUT", "--where")
			c.Lore(noun, "delete", "--where", `body="alpha one"`).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM")
			c.Lore(noun, "delete", "--where", `body="alpha one"`, "--yes").OK(t)
			if l := knowListRows(t, c, k); l.has(a) || !l.has(b) {
				t.Fatalf("list after bulk delete: %v", l.ids())
			}
			c.Lore(noun, "delete", "--where", `body="alpha one"`, "--yes").OK(t) // nothing matches the second time
		})
	}
}

func TestCampaignKnowledge_EditBlankRefused(t *testing.T) {
	scenParallel(t)
	for _, k := range campKnowKinds() {
		if !k.canEdit {
			continue
		}
		t.Run(k.noun, func(t *testing.T) {
			_, _, c := knowProject(t)
			id := campMustAdd(t, c, k, "Keep me", "keep this body")
			flags := []string{"--body"}
			if k.editFlag != "" {
				flags = append(flags, k.editFlag)
			}
			before := campTreeHash(t, c.Dir)
			for _, f := range flags {
				for _, v := range []string{"   ", "\t\n"} {
					r := c.Lore(k.noun, "edit", id, f+"="+v)
					if r.Code != 20 || !strings.Contains(r.Stderr, "E_") {
						t.Fatalf("edit %s=%q: want a refusal (exit 20): %s", f, v, r)
					}
				}
			}
			row := knowShowRow(t, c, k, id)
			if row.Body != "keep this body" || (k.titleKey != "" && row.title(k) != "Keep me") {
				t.Fatalf("a refused blank edit changed the row: %+v", row)
			}
			if campTreeHash(t, c.Dir) != before {
				t.Fatalf("a refused blank edit wrote to the repo")
			}
		})
	}
}
