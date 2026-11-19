package host

// handdelete.go — a row file deleted outside lore (devco S-8). lore never
// deletes a row file in a repo: `lore <noun> delete` moves the row to the
// trash (the file stays, marked deleted, with an audit entry), a purge
// leaves a tombstone, `lore move` tombstones the old copy. So a row file
// that is gone from the work tree while HEAD has it was removed by hand
// (`rm .lore/data/rules/<id>.json`, an agent tidying "stale files"), and
// committing it drops the row from every clone, its rule from LORE.md,
// with no trash copy and no audit entry.
//
//	pre-commit: deleted row files (not renamed: no other changed file has
//	the id) ─▶ refused, naming each row (kind, id, its summary) with
//	`git checkout HEAD -- <path>` to bring it back and `lore <noun> delete
//	--id <id> --yes` to delete it on purpose; EnvAllowRowDrop=1 commits
//	the deletion anyway
//	doctor: the same rows, as a warning with the same next steps
//
// A git error says nothing: this guards a git command, it never breaks one.

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
)

// handDeletedRow is one row file gone from the work tree that HEAD has.
type handDeletedRow struct {
	droppedRow
	// label names the row for a person: its kind ("must rule") and its
	// summary, title or name in quotes, from HEAD's copy.
	label string
}

// rowLabelFields are the fields of a row file that name it, best first.
type rowLabelFields struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Title    string `json:"title"`
	Name     string `json:"name"`
	Body     string `json:"body"`
}

// maxLabelRunes cuts a long summary or body in a label.
const maxLabelRunes = 60

// rowLabel is the label of the row file b of table (kind and its summary).
func rowLabel(table string, b []byte) string {
	kind := tableNoun(table)
	if kind == "" {
		kind = table + " row"
	}
	var f rowLabelFields
	if json.Unmarshal(b, &f) != nil {
		return kind
	}
	if f.Severity != "" {
		kind = f.Severity + " " + kind
	}
	text := f.Summary
	for _, t := range []string{f.Title, f.Name, f.Body} {
		if text == "" {
			text = t
		}
	}
	text, _, _ = strings.Cut(strings.TrimSpace(text), "\n")
	if r := []rune(text); len(r) > maxLabelRunes {
		text = string(r[:maxLabelRunes]) + "…"
	}
	if text == "" {
		return kind
	}
	return kind + " \"" + text + "\""
}

// handDeletedRows are the row files of changed (paths relative to root,
// as changedUnder lists them) that are gone from the work tree while HEAD
// has them, and whose id no other listed file still holds (a rename or a
// case fix keeps the row).
func handDeletedRows(ctx context.Context, repo *gitref.Repo, root string, changed []string) []handDeletedRow {
	present := map[string]bool{} // ids of the listed files still in the work tree
	var gone []string
	for _, p := range changed {
		if !isRowFile(p) {
			continue
		}
		id := strings.ToLower(strings.TrimSuffix(path.Base(p), rowFileExt))
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			present[id] = true
			continue
		}
		gone = append(gone, p)
	}
	if len(gone) == 0 {
		return nil
	}
	heads := headVersions(ctx, repo, root, gone)
	var out []handDeletedRow
	for _, p := range gone {
		b, ok := heads[p]
		if !ok || present[strings.ToLower(strings.TrimSuffix(path.Base(p), rowFileExt))] {
			continue
		}
		d := droppedRow{path: p}
		out = append(out, handDeletedRow{droppedRow: d, label: rowLabel(d.table(), b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// handDeletedList is one line per row: label, id and file.
func handDeletedList(rows []handDeletedRow) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString("\n  " + r.label + " " + r.id() + " (" + r.path + ")")
	}
	return b.String()
}

// handDeletedSteps are the next steps for rows: bring each file back, or
// delete the row through lore.
func handDeletedSteps(rows []handDeletedRow) string {
	paths := make([]string, len(rows))
	for i, r := range rows {
		paths[i] = r.path
	}
	back := "git checkout HEAD -- " + strings.Join(paths, " ")
	return "bring it back: " + back + "; to delete it on purpose, bring it back, then " + deleteHint(rows[0].droppedRow) + " (each row: the trash keeps a copy and the audit log names you)"
}

// handDeleteError is the pre-commit refusal for rows deleted by hand (nil
// when there are none or EnvAllowRowDrop is set).
func handDeleteError(rows []handDeletedRow) error {
	if len(rows) == 0 || os.Getenv(EnvAllowRowDrop) == envAllowOn {
		return nil
	}
	return errs.New(errs.NeedsConfirm, "refusing to commit: this commit deletes "+countRows(len(rows))+" by removing the file, not with lore (no trash copy, no audit entry, and every clone loses it):"+handDeletedList(rows)).
		WithHint(handDeletedSteps(rows) + ", then commit again; to commit the deletion as it is: `" + EnvAllowRowDrop + "=1 git commit`")
}

// handDeletedWarning is doctor's line for rows deleted by hand ("" when
// there are none).
func handDeletedWarning(rows []handDeletedRow) string {
	if len(rows) == 0 {
		return ""
	}
	return countRows(len(rows)) + " deleted by removing the file, not with lore (no trash copy, no audit entry; a commit of it is refused):" + handDeletedList(rows) + "\n  " + handDeletedSteps(rows)
}
