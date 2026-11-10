package wire

// doctor_projects.go — doctor's warning for a repository that holds more
// than one live project row (two `lore init`s on separate branches, then a
// merge; v0.1.17 doctor warned the same). Rows of the second project then
// fail the commit hook's "row of another project" check until `lore sync
// fix-projects` keeps one project and re-points the rows.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckProjects is the check name of the project-rows warning.
const DoctorCheckProjects DoctorCheck = "projects"

// fixProjectsCommand settles two project rows in one repository.
const fixProjectsCommand = "lore sync fix-projects"

// DoctorProjects warns when the repo store holds two or more live project
// rows. A repo that cannot be listed adds nothing (the cache and store
// checks report it).
func (a *App) DoctorProjects(ctx context.Context) ([]DoctorFinding, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, nil
	}
	rows, err := eng.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil || len(rows) < 2 {
		return nil, nil
	}
	idList := make([]string, 0, len(rows))
	for _, r := range rows {
		idList = append(idList, r.ID.String())
	}
	slices.Sort(idList)
	return []DoctorFinding{{Check: DoctorCheckProjects, Level: DoctorWarning, Plain: true,
		Message: fmt.Sprintf("%d project rows in this repository (%s; two `lore init`s merged?): run `%s` to keep one and re-point the rows (preview with --dry-run)",
			len(rows), strings.Join(idList, ", "), fixProjectsCommand)}}, nil
}
