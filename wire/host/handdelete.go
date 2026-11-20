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
// A row file lore itself removed because its row now lives in a store
// (`lore store adopt --remove-source`, wire/movedout.go) is no hand
// delete: the commit takes it, with one line naming the store
// (devco:D-710).
//
// A git error says nothing: this guards a git command, it never breaks one.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/wire"
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

// withoutMovedOut splits rows into the ones removed by hand and the ones
// lore removed because they now live in a store (wire.MovedOut, D-710);
// places are the store/folders of the moved ones, by the commands that
// removed them (wire.MovedOutBy, D-736).
func withoutMovedOut(ctx context.Context, root string, rows []handDeletedRow) (hand, moved []handDeletedRow, places, by []string) {
	if len(rows) == 0 {
		return nil, nil, nil, nil
	}
	marked, cmds := wire.MovedOut(ctx, root), wire.MovedOutBy(ctx, root)
	at, how := map[string]string{}, map[string]string{}
	for _, r := range rows {
		if place, ok := marked[r.path]; ok {
			moved = append(moved, r)
			at[r.path], how[r.path] = place, cmds[r.path]
			continue
		}
		hand = append(hand, r)
	}
	return hand, moved, wire.MovedOutPlaces(at), wire.MovedOutPlaces(how)
}

// movedByWords names the commands of by in backticks ("`lore move`, `lore
// doctor --fix`").
func movedByWords(by []string) string {
	q := make([]string, 0, len(by))
	for _, b := range by {
		q = append(q, "`"+b+"`")
	}
	return strings.Join(q, ", ")
}

// movedOutLine is the hook's line for row files lore removed because
// their rows live in places now ("" when there are none); by names the
// commands that removed them.
func movedOutLine(moved []handDeletedRow, places, by []string) string {
	if len(moved) == 0 {
		return ""
	}
	return "lore: this commit removes " + countRows(len(moved)) + " from .lore/data that " + movedByWords(by) + " moved to " + strings.Join(places, ", ") + " (the rows live there now)"
}

// movedOutDoctorLine is doctor's note for the same rows, with the command
// that commits the removal from anywhere: opsstores.AdoptCommitCommand,
// the one adopt prints and --commit runs, so tidied (files outside
// .lore/data adopt --move removed and nobody staged yet, movedOutTidied)
// are staged with the rows; `add -A .lore/data` alone left a removed
// .lore/LORE.md out of the commit, still tracked (devco D-719).
func movedOutDoctorLine(root string, moved []handDeletedRow, places, by, tidied []string) string {
	where := strings.Join(places, ", ")
	with := ""
	if len(tidied) > 0 {
		with = " (" + strings.Join(tidied, ", ") + " went with them)"
	}
	return countRows(len(moved)) + " removed from .lore/data by " + movedByWords(by) + " live in " + where + " now" + with + "; commit the removal: " + opsstores.AdoptCommitCommand(root, where, tidied...)
}

// movedOutTidied are the files outside .lore/data that `lore store adopt
// --move` removes beside the rows (opsstores.AdoptTidyOutside) and that
// are gone from root's work tree while git still tracks them unstaged:
// the paths doctor's commit command must name. A deletion already staged
// is left out, since `git add` of a path in neither the work tree nor the
// index fails (D-715). A git error lists nothing.
func movedOutTidied(ctx context.Context, repo *gitref.Repo, root string) []string {
	var out []string
	for _, rel := range opsstores.AdoptTidyOutside() {
		changed, staged, _, err := changedUnderStaged(ctx, repo, root, rel)
		if err != nil {
			continue
		}
		for _, p := range changed {
			if _, serr := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); p == rel && !staged[p] && errors.Is(serr, fs.ErrNotExist) {
				out = append(out, p)
			}
		}
	}
	return out
}

// rowPaths are the paths of rows.
func rowPaths(rows []handDeletedRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.path
	}
	return out
}
