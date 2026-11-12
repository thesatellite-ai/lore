package host

// precommit_store.go — `lore sync hook pre-commit` run by the pre-commit
// block lore puts in a store clone's .git/hooks (plain files round, brief
// 2.4, D-g; adapters/backend/git/handgit.go). A person committing by hand
// in a store gets the checks lore's own writes pass:
//
//	git commit ─▶ .git/hooks/pre-commit ─▶ lore sync hook pre-commit
//	   ─▶ every staged row file (<folder>/<table>/<id>.json): one JSON
//	      object, no conflict markers, a row of the table and id its path
//	      names, every field valid for its table
//	   ─▶ refused with file:line and the reason, nothing committed
//	   ─▶ then the block runs `lore check leaks --staged` (the secret scan
//	      of every staged file, wire/hooksecrets.go)
//
// Lore's own commits never run the hook (each git call lore makes passes
// core.hooksPath at a folder that does not exist); they were checked
// before they were written.

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// storeCommitHint is the next step of a refused hand commit in a store.
const storeCommitHint = "fix each file (a row file is one JSON object; `lore edit <id>` edits a row safely), or put it back with `git checkout HEAD -- <path>`; then commit again"

// storePreCommit checks the staged row files of the store clone at top.
func (h *liveHost) storePreCommit(ctx context.Context, top string) error {
	repo, err := gitref.Open(ctx, top, h.cfg.Git)
	if err != nil {
		return nil // not a git checkout (a hook copied around)
	}
	env := hookIndexEnv(ctx, repo, top)
	out, err := repo.Run(ctx, gitref.Invocation{Dir: top, Env: env, Args: []string{"-c", "core.quotePath=false", "diff", "--cached", "--name-only", "-z", "--diff-filter=ACMR"}})
	if err != nil {
		return errs.Wrap(errs.Internal, "pre-commit: list the staged files of "+top, err)
	}
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return errs.Wrap(errs.Internal, "pre-commit: core registry", err)
	}
	var bad []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		_, rel, ok := rowfs.LayoutStore.SplitRowPath(p)
		if !ok {
			continue // not a row file: the secret scan still reads it
		}
		b, rerr := repo.Run(ctx, gitref.Invocation{Dir: top, Env: env, Args: []string{"cat-file", "blob", ":" + p}})
		if rerr != nil {
			return errs.Wrap(errs.Internal, "pre-commit: read the staged "+p, rerr)
		}
		if line, why := badStoreRow(reg, rel, b); why != "" {
			bad = append(bad, p+":"+strconv.Itoa(line)+": "+why)
		}
	}
	if len(bad) > 0 {
		return errs.New(errs.InvalidInput, "refusing to commit row files lore cannot accept:\n  "+strings.Join(bad, "\n  ")).WithHint(storeCommitHint)
	}
	return nil
}

// badStoreRow is why a staged row file cannot be committed, with the line
// it points at ("" = fine). rel is "<table>/<id>.json".
func badStoreRow(reg *schema.Registry, rel string, b []byte) (int, string) {
	if why := brokenLoreFile(b); why != "" {
		return firstMarkerLine(b), why
	}
	table, id, _ := rowfs.ParseRowPath(rel)
	r, err := row.Decode(b)
	if err != nil {
		return 1, "not a row: " + err.Error()
	}
	if r.Table != table || r.ID != id {
		return keyLine(b, "id"), "the file holds row " + r.ID.String() + " of table " + string(r.Table) + ", but its path names " + id.String() + " of table " + string(table)
	}
	def, ok := reg.Table(table)
	if !ok {
		return 1, "table " + string(table) + " is not one this lore knows"
	}
	if r.Deleted {
		return 0, "" // a tombstone holds what is left of the row
	}
	if _, err := validate.Fields(def, engine.RetypeFields(def, r.Fields), validate.Whole); err != nil {
		msg := err.Error()
		var e *errs.Error
		if errors.As(err, &e) {
			msg = e.Message
		}
		line := 1
		for _, f := range def.Fields {
			if strings.Contains(msg, f.Name) {
				if l := keyLine(b, f.Name); l > 1 {
					line = l
					break
				}
			}
		}
		return line, msg
	}
	return 0, ""
}

// firstMarkerLine is the line of the first conflict marker in b (1 when
// there is none: invalid JSON is reported at the file).
func firstMarkerLine(b []byte) int {
	for i, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "<<<<<<<") || strings.HasPrefix(l, "=======") || strings.HasPrefix(l, ">>>>>>>") {
			return i + 1
		}
	}
	return 1
}

// keyLine is the line of the first `"key":` in b (1 when absent).
func keyLine(b []byte, key string) int {
	i := strings.Index(string(b), strconv.Quote(key)+":")
	if i < 0 {
		return 1
	}
	return strings.Count(string(b[:i]), "\n") + 1
}
