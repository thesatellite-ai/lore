package git

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Versions implements store.RowHistory (OV62): the committed versions of
// one row file, oldest first, in both modes (a store clone's own commits;
// the code repo's commits the person made). Rows never change path (a row
// file is always <data>/<table>/<id>.json and a move between stores writes
// a new file), so git log does not follow renames.
//
//	Versions ─► HEAD? (unborn: no history) ─► git log --format over the
//	         row file path ─► one cat-file batch "<commit>:./<path>"
//	         ─► DecodeRowFile (undecodable / deleted versions left out)
func (b *Backend) Versions(ctx context.Context, table row.Table, id ids.AnyID) ([]store.RowVersion, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	if !table.Valid() || id.IsZero() {
		return nil, errs.New(errs.InvalidInput, "git: history needs a table and an id")
	}
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return nil, err
	}
	path := b.folder.StorePath(table, id)
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "--no-renames", "--format=" + historyFormat, h, "--", path}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read the row's history", err)
	}
	commits := parseHistory(out)
	slices.Reverse(commits) // git log is newest first
	names := make([]string, len(commits))
	for i, c := range commits {
		// "./" makes the path relative to b.work (repo mode may sit in a
		// monorepo subfolder), not to the repository top.
		names[i] = c.Commit + ":./" + path
	}
	blobs, err := b.readBlobs(ctx, names)
	if err != nil {
		return nil, err
	}
	rel := rowfs.RowPath(table, id)
	vers := make([]store.RowVersion, 0, len(commits))
	for i, c := range commits {
		doc, ok := blobs[names[i]]
		if !ok || len(doc) > rowfs.MaxFileSize {
			continue // the commit deleted the file, or it is over the cap
		}
		r, derr := rowfs.DecodeRowFile(rel, doc)
		if derr != nil {
			continue // a hand-edited or conflicted version: never guessed
		}
		c.Row = r
		vers = append(vers, c)
	}
	return vers, nil
}

var _ store.RowHistory = (*Backend)(nil)

// historySep separates the fields of one log line (a unit separator never
// appears in a commit id, an ISO date, or a sane name or email).
const historySep = "\x1f"

// historyFormat is one log line: commit, author date (strict ISO), author
// name, author email.
const historyFormat = "%H" + historySep + "%aI" + historySep + "%an" + historySep + "%ae"

// historyFields is the field count of one log line.
const historyFields = 4

// parseHistory reads git log lines in historyFormat; a malformed line is
// skipped (git wrote it, so this only drops a name holding the separator).
func parseHistory(out []byte) []store.RowVersion {
	var vers []store.RowVersion
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, historySep)
		if len(f) != historyFields || !commitIDPattern.MatchString(f[0]) {
			continue
		}
		at, err := time.Parse(time.RFC3339, f[1])
		if err != nil {
			continue
		}
		vers = append(vers, store.RowVersion{Commit: f[0], At: at.UTC(), Author: f[2], Email: f[3]})
	}
	return vers
}
