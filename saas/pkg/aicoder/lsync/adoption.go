package lsync

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// metaAdoptedProjects holds the project adoptions (E1) whose core-owned row
// files still name the local project id: one "local shared" pair per line.
// ADOPT writes it in its transaction (adoptProjectID); the CLI re-points
// the files through the 0.1.18 core and then clears it. Why a durable mark
// and not only the pass's Report: the core rewrite runs after the pass
// commits, and a process that dies in between must not leave the clone's
// row files hanging off a project nobody else has.
const metaAdoptedProjects = "adopted_projects"

// ProjectAdoption is one local project id ADOPT replaced by the shared one
// _meta.json pins, in lore.db only (see metaAdoptedProjects).
type ProjectAdoption struct {
	Local  string `json:"local"`
	Shared string `json:"shared"`
}

// adoptionSep separates the two ids of one stored pair (ids hold no space).
const adoptionSep = " "

func addProjectAdoption(ctx context.Context, q execer, a ProjectAdoption) error {
	cur, err := readAdoptions(ctx, q)
	if err != nil {
		return err
	}
	if slices.Contains(cur, a) {
		return nil
	}
	return writeAdoptions(ctx, q, append(cur, a))
}

func readAdoptions(ctx context.Context, q execer) ([]ProjectAdoption, error) {
	v, ok, err := getMeta(ctx, q, metaAdoptedProjects)
	if err != nil || !ok {
		return nil, err
	}
	var out []ProjectAdoption
	for _, line := range strings.Split(v, "\n") {
		local, shared, found := strings.Cut(strings.TrimSpace(line), adoptionSep)
		if !found || local == "" || shared == "" {
			continue
		}
		out = append(out, ProjectAdoption{Local: local, Shared: shared})
	}
	return out, nil
}

func writeAdoptions(ctx context.Context, q execer, as []ProjectAdoption) error {
	if len(as) == 0 {
		return deleteMeta(ctx, q, metaAdoptedProjects)
	}
	lines := make([]string, 0, len(as))
	for _, a := range as {
		lines = append(lines, a.Local+adoptionSep+a.Shared)
	}
	return setMeta(ctx, q, metaAdoptedProjects, strings.Join(lines, "\n"))
}

// PendingProjectAdoptions lists the adoptions whose core-owned row files
// have not been re-pointed to the shared project yet (metaAdoptedProjects).
// Empty on a DB that never adopted, or once every one was cleared.
func PendingProjectAdoptions(ctx context.Context, db *sql.DB) ([]ProjectAdoption, error) {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return nil, err
	}
	return readAdoptions(ctx, db)
}

// ClearProjectAdoption removes one adoption from the pending list, once the
// caller re-pointed every core-owned row of a.Local to a.Shared. Clearing
// one that is not pending is a no-op (a re-run).
func ClearProjectAdoption(ctx context.Context, db *sql.DB, a ProjectAdoption) error {
	cur, err := readAdoptions(ctx, db)
	if err != nil {
		return err
	}
	next := slices.DeleteFunc(slices.Clone(cur), func(x ProjectAdoption) bool { return x == a })
	if len(next) == len(cur) {
		return nil
	}
	if err := writeAdoptions(ctx, db, next); err != nil {
		return fmt.Errorf("lsync: clear project adoption %s: %w", a.Local, err)
	}
	return nil
}
