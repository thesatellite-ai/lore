//go:build stories && !windows

package stories

// O_docs_import_test.go — devco:D-940 (bug hunt H-189, the owner's F-1)
// on the owner-shaped machine (story O): `lore docs import --from
// TECHNICAL.md` drafts as-built notes from the repo's own doc, a memory
// per section (titled with its heading path) and a proposed decision per
// stated choice, each carrying its doc line (and the code line the text
// names) as fact_evidence and fact_status=unverified until someone checks
// it.
//
//   - --dry-run lists the notes and writes nothing (no store commit);
//   - the import lands every note as ONE commit in the target store;
//   - a re-import adds nothing (every note is skipped as already noted);
//   - the printed check command runs as printed and the note leaves the
//     unverified list;
//   - a doc holding a secret refuses the whole import, nothing written,
//     and the secret answer it names (--secret-unstored) runs as printed;
//     no output ever shows the token;
//   - devco:D-962 (bug hunt H-221, issue #162): the issue's doc drafts one
//     note per section, never one per paragraph: a list stays with its
//     lead-in, a Decision. / Considered. / Why. section is one proposed
//     decision, the contents and license sections are left out, and a
//     paragraph the store already holds is dropped from its section only.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// oDocsImportID finds the ids an import printed.
var oDocsImportID = regexp.MustCompile(`(?m)^  (memory mem_[0-9a-f]+|decision dec_[0-9a-f]+) `)

func TestStoryODocsImportDraftsUnverifiedNotes(t *testing.T) {
	s := New(t, "O")
	kb := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-kb")
	orbit := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/orbit"), func(dir string) {
		PlainRepo(t, dir)
		if err := os.MkdirAll(filepath.Join(dir, "internal", "queue"), 0o755); err != nil {
			t.Fatal(err)
		}
		for rel, body := range map[string]string{
			"internal/queue/worker.go": "package queue\n\n// Run pulls jobs.\nfunc Run() {}\n",
			"TECHNICAL.md": "# Orbit\n\n## How it works\n\n" +
				"The worker pulls jobs from the queue every five seconds and parks a job after three failed tries (internal/queue/worker.go:4).\n\n" +
				"- Invoices are rendered as PDF and kept for ninety days.\n- ok\n\n" +
				"```sh\ngo run ./cmd/orbit serve --port 8080 now\n```\n\n## Decisions\n\n" +
				"We chose Postgres advisory locks over Redis locks because the service already runs on Postgres.\n",
		} {
			if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	code := ada.CloneTo(orbit, ada.Code("orbit"))
	var kbClone string
	var noted []string

	s.Step("D-940/dry-run-lists-and-writes-nothing", func() {
		ada.LoreIn(code, "init").OK(t)
		ada.LoreIn(code, "store", "init", "--name", "kb", "--url", kb.Dir, "--yes").OK(t)
		ada.LoreIn(code, "memory", "add", "--body", "first note of kb", "--to", "kb/notes", "--create", "--yes").OK(t)
		kbClone = oStoreClone(ada, "_local", "lore-kb")
		before := oCommits(t, kbClone)
		ada.LoreIn(code, "docs", "import", "--from", "TECHNICAL.md", "--to", "kb/notes", "--dry-run").OK(t).
			Says(t, "would draft 2 unverified notes from TECHNICAL.md", "memory (new) — Orbit › How it works  [TECHNICAL.md:5, internal/queue/worker.go:4]",
				"decision (new) — We chose Postgres advisory locks over Redis locks because", "[TECHNICAL.md:16]",
				"nothing written (--dry-run)").
			Never(t, "go run", "— ok")
		oCheck(t, "D-940/dry-run-no-commit", oCommits(t, kbClone) == before, "--dry-run made %d commits", oCommits(t, kbClone)-before)
	})

	s.Step("D-940/import-one-commit-unverified-with-evidence", func() {
		before := oCommits(t, kbClone)
		r := ada.LoreIn(code, "docs", "import", "--from", "TECHNICAL.md", "--to", "kb/notes", "--yes").OK(t).
			Says(t, "drafted 2 unverified notes from TECHNICAL.md in 1 store write", "(undo: lore batch undo --id bat_",
				"lore meta set --id <id> --pair fact_status=verified --set fact_verified_at=<YYYY-MM-DD>")
		oCheck(t, "D-940/import-one-commit", oCommits(t, kbClone) == before+1, "the import made %d commits, want 1", oCommits(t, kbClone)-before)
		for _, m := range oDocsImportID.FindAllStringSubmatch(r.Stdout, -1) {
			noted = append(noted, strings.Fields(m[1])[1])
		}
		oCheck(t, "D-940/import-printed-ids", len(noted) == 2, "the import printed %d ids, want 2:\n%s", len(noted), r.Stdout)
		ada.LoreIn(code, "memory", "list", "--meta", "fact_status=unverified", "--to", "kb/notes", "--json").OK(t).
			// fact_evidence is a built-in list key since H-190 (D-941): the import's comma-joined evidence is stored as a list and
			// lists print joined with " | " (meta.Show)
			Says(t, `"fact_evidence": "TECHNICAL.md:5 | internal/queue/worker.go:4"`, `"source_kind": "imported"`, `"source_ref": "TECHNICAL.md:5"`,
				// the section's list is in the same note as the paragraph above it (D-962)
				`- Invoices are rendered as PDF and kept for ninety days.`)
		ada.LoreIn(code, "decision", "list", "--meta", "fact_status=unverified", "--to", "kb/notes").OK(t).Says(t, "[proposed]", "Postgres advisory locks")
	})

	s.Step("D-940/re-import-adds-nothing", func() {
		before := oCommits(t, kbClone)
		ada.LoreIn(code, "docs", "import", "--from", "TECHNICAL.md", "--to", "kb/notes", "--yes").OK(t).
			Says(t, "drafted 0 unverified notes", "skipped 2", "already noted as "+noted[0])
		oCheck(t, "D-940/re-import-no-commit", oCommits(t, kbClone) == before, "a re-import made %d commits", oCommits(t, kbClone)-before)
	})

	s.Step("D-940/the-printed-check-runs-as-printed", func() {
		ada.LoreIn(code, "meta", "set", "--id", noted[0], "--pair", "fact_status=verified", "--set", "fact_verified_at=2026-11-23", "--yes").OK(t)
		ada.LoreIn(code, "memory", "list", "--meta", "fact_status=unverified", "--to", "kb/notes").OK(t).Never(t, noted[0])
		ada.LoreIn(code, "decision", "list", "--meta", "fact_status=unverified", "--to", "kb/notes").OK(t).Says(t, noted[1])
	})

	s.Step("D-940/a-secret-refuses-the-whole-import", func() {
		tok := "ghp_" + strings.Repeat("Zq4", 12) // built at run time: no token-shaped literal in the repo
		code.WriteFile("OPS.md", "# Ops\n\nThe deploy job reads its registry token "+tok+" from the runner environment.\n\n"+
			"The nightly backup runs at two and keeps fourteen copies of the database.\n")
		before := oCommits(t, kbClone)
		r := ada.LoreIn(code, "docs", "import", "--from", "OPS.md", "--to", "kb/notes", "--yes")
		oCheck(t, "D-940/secret-refused", r.Code != 0 && strings.Contains(r.Stderr, "looks like a secret") && strings.Contains(r.Stderr, "--secret-unstored"),
			"an import holding a token was not refused with the answers:\n%s", r)
		oCheck(t, "D-940/secret-nothing-written", oCommits(t, kbClone) == before, "a refused import made %d commits", oCommits(t, kbClone)-before)
		oCheck(t, "D-940/secret-not-echoed", !strings.Contains(r.Stdout+r.Stderr, tok), "the refusal printed the token:\n%s", r)
		// the answer the hint names runs on docs import as printed; the token never reaches the output
		ada.LoreIn(code, "docs", "import", "--from", "OPS.md", "--to", "kb/notes", "--secret-unstored", "--yes").OK(t).
			Says(t, "drafted 1 unverified note from OPS.md", "memory mem_", "— Ops  [OPS.md:3]").Never(t, tok)
		ada.LoreIn(code, "memory", "list", "--meta", "fact_status=unverified", "--to", "kb/notes", "--json").OK(t).Says(t, "${secret:unstored}").Never(t, tok)
	})

	s.Step("D-962/one-note-per-section-of-the-issue-doc", func() {
		code.WriteFile("WIDGET.md", oWidgetDoc)
		ada.LoreIn(code, "memory", "add", "--to", "kb/notes", "--body", "The widget cache keeps entries for 10 minutes and is cleared on every import.", "--yes").OK(t)
		ada.LoreIn(code, "docs", "import", "--from", "WIDGET.md", "--to", "kb/notes", "--dry-run").OK(t).
			Says(t, "memory (new) — widget module: technical notes › Overview  [WIDGET.md:12]",
				"decision (new) — One writer for widget rows  [WIDGET.md:26]", "decision (new) — Batch size  [WIDGET.md:36]",
				"memory (new) — widget module: technical notes › Caching  [WIDGET.md:44]",
				"skipped WIDGET.md:42 — The widget cache keeps entries for 10 minutes and is cleared on every import: already noted as mem_",
				// the per-doc counts: drafted by kind, skipped by reason (H-221 addendum)
				"WIDGET.md: would draft 2 memories, 2 decisions; skipped 1 already stored, 2 boilerplate sections",
				"would draft 4 unverified notes from WIDGET.md, skip 1").
			Never(t, "— Decision", "— Considered", "— Why", "— The import has three phases", "— Parse it", "Contributions welcome", "MIT", "[Overview]")
		ada.LoreIn(code, "docs", "import", "--from", "WIDGET.md", "--to", "kb/notes", "--dry-run", "--json").OK(t).
			Says(t, `"file": "WIDGET.md"`, `"memories": 2`, `"decisions": 2`, `"already_stored": 1`, `"boilerplate": 2`, `"reason": "already_stored"`)
		ada.LoreIn(code, "docs", "import", "--from", "WIDGET.md", "--to", "kb/notes", "--yes").OK(t).
			Says(t, "WIDGET.md: drafted 2 memories, 2 decisions; skipped 1 already stored, 2 boilerplate sections", "drafted 4 unverified notes from WIDGET.md")
		ada.LoreIn(code, "memory", "list", "--meta", "fact_evidence=WIDGET.md:12", "--to", "kb/notes", "--json").OK(t).
			Says(t, `The import has three phases:\n\n1. Fetch the bulk file.\n2. Parse it in batches of 500.\n   - A bad row is skipped, not fatal.`)
		ada.LoreIn(code, "memory", "list", "--meta", "fact_evidence=WIDGET.md:44", "--to", "kb/notes", "--json").OK(t).
			Says(t, `widget module: technical notes › Caching\n\nThings that invalidate it:\n\n- an import,\n- a manual purge.`)
		ada.LoreIn(code, "decision", "list", "--meta", "fact_evidence=WIDGET.md:26", "--to", "kb/notes", "--json").OK(t).
			Says(t, `"title": "One writer for widget rows"`, `"status": "proposed"`,
				`Considered. Direct SQL from the importer.\n\nConsidered. A trigger in the database.\n\nWhy. Checks and history cannot be skipped.`)
	})
}

// oWidgetDoc is the sample doc of issue #162 (D-962): a contents list, an
// overview with a nested numbered list, two decisions written as labelled
// paragraphs, a fact the store already holds, and a license footer.
const oWidgetDoc = `# widget module: technical notes

## Contents

- [Overview](#overview)
- [Decisions](#decisions)
- [Caching](#caching)
- [License](#license)

## Overview

The widget module imports widgets from upstream feeds. It runs inside the worker.

The import has three phases:

1. Fetch the bulk file.
2. Parse it in batches of 500.
   - A bad row is skipped, not fatal.
   - Skipped rows are counted in the step output.
3. Write the rows through the single writer.

## Decisions

### D1. One writer for widget rows

Decision. Every widget write goes through widget.Save.

Considered. Direct SQL from the importer.

Considered. A trigger in the database.

Why. Checks and history cannot be skipped.

### D2. Batch size

Decision. 500 rows per batch.

Considered. 5000.

## Caching

The widget cache keeps entries for 10 minutes and is cleared on every import.

Things that invalidate it:

- an import,
- a manual purge.

## License

MIT. See LICENSE.

Contributions welcome; open a pull request.
`
