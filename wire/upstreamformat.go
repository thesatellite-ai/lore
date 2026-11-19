package wire

// upstreamformat.go — a checkout on the lore 0.1.17 format whose fetched
// remote branch is already migrated (K-11: a checkout 9 commits behind a
// pushed migration ran `lore migrate run` again and made a second
// migration commit that conflicts with the first in every row file).
//
//	local _meta.json format 1 ─┐
//	@{upstream}, origin/HEAD ──┴─▶ `git show <ref>:.lore/data/_meta.json`
//	                               format 2? ─▶ "git pull first" (migrate
//	                               run, migrate run --dry-run, lore fix,
//	                               lore fix --all's block for the repo)
//
// Only refs already fetched are read: no network, so a checkout whose
// remote was migrated after its last fetch still migrates (the team lock
// and the merge driver are the guards there).

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
)

// upstreamMigration is a fetched remote branch already on this binary's
// format while the checkout is older.
type upstreamMigration struct {
	// Ref is the remote branch ("origin/main").
	Ref string
	// Format is that branch's project format (who migrated it, when).
	Format rowfs.ProjectFormat
}

// upstreamMigrated reports the fetched remote branch of the checkout at
// top that is already migrated while the checkout is on an older format:
// the current branch's upstream first, then origin's default branch.
func upstreamMigrated(ctx context.Context, top string) (upstreamMigration, bool) {
	if top == "" {
		return upstreamMigration{}, false
	}
	if local, err := ProjectFormatAt(top); err != nil || !OlderFormat(local) {
		return upstreamMigration{}, false
	}
	for _, ref := range upstreamCandidates(ctx, top) {
		doc, err := (gitRun{dir: top, args: []string{"show", ref + ":" + filepath.ToSlash(filepath.Join(rowfs.LoreDir, rowfs.DataDir, rowfs.MetaFileName))}}).output(ctx)
		if err != nil {
			continue // the branch holds no _meta.json: nothing to compare
		}
		pf, perr := rowfs.ParseProjectFormat(doc)
		if perr != nil || pf.Format < rowfs.ProjectFormatCurrent {
			continue
		}
		return upstreamMigration{Ref: ref, Format: pf}, true
	}
	return upstreamMigration{}, false
}

// upstreamCandidates are the remote-tracking branches compared: the
// current branch's upstream, then origin/HEAD's branch, each once.
func upstreamCandidates(ctx context.Context, top string) []string {
	var out []string
	add := func(args ...string) {
		b, err := (gitRun{dir: top, args: args}).output(ctx)
		if err != nil {
			return
		}
		if s := strings.TrimSpace(string(b)); s != "" && s != "HEAD" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	add("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	add("symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD")
	return out
}

// pullFirstLine is the one line of a checkout whose fetched remote branch
// is already migrated: the team did it, so git pull is the step, never a
// second migration.
func pullFirstLine(top string, u upstreamMigration) string {
	name := filepath.Base(top)
	who := ""
	switch {
	case u.Format.MigratedBy != "" && u.Format.MigratedAt != "":
		who = ", migrated by " + u.Format.MigratedBy + " at " + u.Format.MigratedAt
	case u.Format.MigratedBy != "":
		who = ", migrated by " + u.Format.MigratedBy
	}
	return name + " is on the lore 0.1.17 format here, but " + u.Ref + " is already on " + rowfs.FormatName(u.Format.Format) + who +
		": run git pull first and do not migrate here (a second migration conflicts with the first in every row file)"
}

// pullFirstErr is migrate run's refusal in such a checkout.
func pullFirstErr(top string, u upstreamMigration) error {
	return errs.New(errs.Refused, "lore migrate run: "+pullFirstLine(top, u)).
		WithHint("git pull (then lore doctor); lore migrate run is for the one person who migrates the team's project")
}

// MigratePullFirst implements opsmigrate.PullFirstGate: the refusal of a
// run in a checkout whose fetched remote branch is already migrated.
func (a *App) MigratePullFirst(ctx context.Context) error {
	top := gitTopLevel(ctx, a.projectDir())
	if u, ok := upstreamMigrated(ctx, top); ok {
		return pullFirstErr(top, u)
	}
	return nil
}
