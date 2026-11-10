package wire

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsmember "github.com/thesatellite-ai/lore/ops/member"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// FindProject resolves a <project> reference of the member ops
// (TEAM_PROFILES 3.5) across every store the App can read: a project row
// lives in its own folder or repo, which the roster store does not hold
// (project_members.project_id is AllowDangling, 2.6), so the roster alone
// cannot resolve a name.
//
// Which App: the roster's (rosterApp), so outside a repo the store-only
// App answers with the org store it opened, and inside a repo the full
// App answers with every attached store. A prj_ id matches by id, anything
// else by exact name; copies of one row in several stores count once. No
// match is E_NOT_FOUND; a name two different projects share is
// E_PROJECT_NAME_COLLISION (fail closed: a membership never lands on a
// guessed project).
var _ opsmember.ProjectFinder = (*App)(nil)

// FindProject implements opsmember.ProjectFinder.
func (a *App) FindProject(ctx context.Context, ref string) (opsmember.Project, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return opsmember.Project{}, errs.New(errs.InvalidInput, "no project").WithHint("give a project name or prj_ id")
	}
	r, err := a.repoRoster(ctx)
	if err != nil {
		return opsmember.Project{}, err
	}
	x, done, err := a.rosterApp(ctx, r)
	if err != nil {
		return opsmember.Project{}, err
	}
	defer done()
	st, err := x.state(ctx)
	if err != nil {
		return opsmember.Project{}, err
	}
	byID := strings.HasPrefix(ref, string(ids.PrefixProject)+actorIDSep)
	hits := map[ids.AnyID]row.Row{}
	for _, src := range st.eng.Stores() {
		rows, lerr := st.eng.List(ctx, src, schema.TableProjects)
		if lerr != nil {
			return opsmember.Project{}, lerr
		}
		for _, p := range rows {
			if p.Deleted {
				continue
			}
			if (byID && p.ID.String() == ref) || (!byID && fieldText(p, rosterFieldName) == ref) {
				hits[p.ID] = p
			}
		}
	}
	switch len(hits) {
	case 0:
		return opsmember.Project{}, errs.New(errs.NotFound, "no project "+ref+" in the stores this repo reads").
			WithHint("lore project list shows the projects; name one by its name or prj_ id")
	case 1:
		for _, p := range hits {
			id, perr := ids.As[ids.Project](p.ID)
			if perr != nil {
				return opsmember.Project{}, errs.Wrap(errs.Internal, "project id "+p.ID.String(), perr)
			}
			return opsmember.Project{ID: id, Name: fieldText(p, rosterFieldName)}, nil
		}
	}
	names := make([]string, 0, len(hits))
	for id := range hits {
		names = append(names, id.String())
	}
	slices.Sort(names)
	return opsmember.Project{}, errs.New(errs.ProjectNameCollision, "several projects are named "+ref+": "+strings.Join(names, ", ")).
		WithHint("name it by its prj_ id")
}

// ProjectName implements profilestore.ProjectNamer for the previews that
// list memberships (offboard, rejoin, actor show): FindProject by prj_ id,
// so the name comes from whichever attached store holds the project row.
// Anything that is not a prj_ id, or that FindProject cannot answer
// (no such row here, a store that cannot be read), is not found and the
// caller shows the id.
var _ profilestore.ProjectNamer = (*App)(nil)

// ProjectName implements profilestore.ProjectNamer.
func (a *App) ProjectName(ctx context.Context, id string) (string, bool) {
	if !strings.HasPrefix(id, string(ids.PrefixProject)+actorIDSep) {
		return "", false // FindProject would match a name; a label asks by id only
	}
	p, err := a.FindProject(ctx, id)
	if err != nil || p.Name == "" {
		return "", false
	}
	return p.Name, true
}
