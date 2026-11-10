package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// landNamedHint is the next step when `lore land run <id>` names no row.
const landNamedHint = "copy the id from `lore live show` (live rows) or a list such as `lore task list`"

// landBadID refuses a named land target that cannot be a row id
// (E_INVALID_ID, exit 20, the ops/internal/lookup rule). Without it
// `lore land run nope` answered "nothing to land: main already holds
// every named row" and exit 0, a claim about a row that does not exist.
func landBadID(id string) error {
	if id == "" {
		return nil
	}
	if _, err := ids.ParseAny(strings.TrimSpace(id)); err != nil {
		return errs.New(errs.InvalidID, strings.TrimSpace(id)+" is not a row id").WithHint(landNamedHint)
	}
	return nil
}

// landNamedMissing is E_NOT_FOUND when a landing pass that landed nothing
// was named a well-formed id no place holds: not live here, not on the
// remote default branch, not in this checkout's .lore/data. nil when the
// row exists somewhere (then "main already holds every named row" is
// true, or the row is not live and needs no landing).
func (s *liveSession) landNamedMissing(ctx context.Context, root, folder, id string) error {
	id = strings.TrimSpace(id)
	table, ok := tableOfID(id)
	if !ok {
		return nil // a dynamic kind: no table to look in, keep the pass's answer
	}
	if _, tree, err := s.ref.LocalTree(ctx); err == nil {
		rows, _ := tree.Rows(folder)
		for k := range rows {
			if k.ID == id {
				return nil
			}
		}
	}
	rel := table + "/" + id + ".json"
	branch := s.defaultBranch(ctx)
	mainRef := "refs/remotes/" + s.ref.RemoteName + "/" + branch
	if _, found, err := s.repo.ReadFile(ctx, mainRef, mainDataPath(folder, rel)); err != nil || found {
		return nil // unreadable main: never claim the row is missing
	}
	if _, err := os.Stat(filepath.Join(root, ".lore", "data", filepath.FromSlash(rel))); err == nil {
		return nil
	}
	return errs.New(errs.NotFound, "no row "+id+" to land: it is not live here, not on "+branch+" and not in .lore/data").
		WithHint(landNamedHint)
}
