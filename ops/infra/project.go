package infra

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// ProjectListIn is the input of project.list.
type ProjectListIn struct {
	Store string `json:"store,omitempty" flag:"store" desc:"only projects of this store (default every attached store)"`
}

// Project is one project in v0.1.17's --json list form (the ent Project
// struct; the machine-local last_active_at no longer travels in a row).
type Project struct {
	ID         string `json:"id"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	Name       string `json:"name"`
	OriginURL  string `json:"origin_url,omitempty"`
	ArchivedAt string `json:"archived_at,omitempty"`
	Store      string `json:"_source,omitempty"`
	// line is the v0.1.17 human line of this project.
	line string
}

// ProjectListOut is v0.1.17's list envelope.
type ProjectListOut struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Count         int       `json:"count,omitempty"`
	Data          []Project `json:"data"`
}

// Text is v0.1.17's list: "<id>  <name>[  <origin>][ [archived]]".
func (o ProjectListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no projects)\n"
	}
	var b strings.Builder
	for _, p := range o.Data {
		b.WriteString(p.line)
	}
	return b.String()
}

// RepoBrief is one repo inside `project show --json` (v0.1.17 repoBrief).
type RepoBrief struct {
	ID          string `json:"id"`
	MountName   string `json:"mount_name"`
	DisplayName string `json:"display_name,omitempty"`
	OriginURL   string `json:"origin_url,omitempty"`
	ArchivedAt  string `json:"archived_at,omitempty"`
}

// ProjectDetail is v0.1.17's projectWithRepos.
type ProjectDetail struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	OriginURL  string      `json:"origin_url,omitempty"`
	CreatedAt  string      `json:"created_at"`
	ArchivedAt string      `json:"archived_at,omitempty"`
	Repos      []RepoBrief `json:"repos"`
	// created is the human creation time.
	created string
}

// ProjectShowOut is v0.1.17's show envelope.
type ProjectShowOut struct {
	SchemaVersion int           `json:"schema_version"`
	Kind          string        `json:"kind"`
	Data          ProjectDetail `json:"data"`
}

// Text is v0.1.17's show block (the "active:" line was the machine-local
// last_active_at and is gone).
func (o ProjectShowOut) Text() string {
	p := o.Data
	var b strings.Builder
	b.WriteString("Project: " + p.ID + "\n")
	b.WriteString("  name:    " + p.Name + "\n")
	if p.OriginURL != "" {
		b.WriteString("  origin:  " + p.OriginURL + "\n")
	}
	b.WriteString("  created: " + p.created + "\n")
	if len(p.Repos) > 0 {
		b.WriteString("\nRepos:\n")
		for _, r := range p.Repos {
			b.WriteString("  " + r.ID + "  " + r.MountName + "\n")
		}
	}
	return b.String()
}

// sortByCreated orders rows oldest first (stable: ties keep store order).
func sortByCreated(rows []work.Sourced) []work.Sourced {
	out := slices.Clone(rows)
	slices.SortStableFunc(out, func(a, b work.Sourced) int { return a.Row.CreatedAt.Compare(b.Row.CreatedAt) })
	return out
}

// projectList lists the live projects of every attached store, oldest
// first (v0.1.17 ordered by created_at).
func (o ops) projectList(ctx context.Context, in ProjectListIn) (ProjectListOut, error) {
	rows, err := work.ListAll(ctx, o.env, schema.TableProjects, in.Store)
	if err != nil {
		return ProjectListOut{}, err
	}
	out := ProjectListOut{SchemaVersion: SchemaVersion, Kind: KindProjectList, Data: []Project{}}
	for _, s := range sortByCreated(rows) {
		r := s.Row
		out.Data = append(out.Data, Project{
			ID: r.ID.String(), CreatedAt: timeOf(r, true), UpdatedAt: timeOf(r, false), Name: work.Field(r, fieldName),
			OriginURL: work.Field(r, fieldOrigin), ArchivedAt: work.TimeText(r, fieldArchivedAt), Store: sourceOf(s.Source),
			line: listLine(r, work.Field(r, fieldName)),
		})
	}
	out.Count = len(out.Data)
	return out, nil
}

// projectShow shows one project (a prj_ id or a name) with its repos.
func (o ops) projectShow(ctx context.Context, in IDIn) (ProjectShowOut, error) {
	pid, err := work.Project(ctx, o.env, NounProject, strings.TrimSpace(in.ID))
	if err != nil {
		return ProjectShowOut{}, err
	}
	src, r, err := work.Get(ctx, o.env, NounProject, schema.TableProjects, pid.Any())
	if err != nil {
		return ProjectShowOut{}, err
	}
	d := ProjectDetail{
		ID: r.ID.String(), Name: work.Field(r, fieldName), OriginURL: work.Field(r, fieldOrigin),
		CreatedAt: r.CreatedAt.UTC().Format(work.TimeLayout), ArchivedAt: work.TimeText(r, fieldArchivedAt),
		created: r.CreatedAt.UTC().Format(showTimeLayout), Repos: []RepoBrief{},
	}
	repos, err := o.env.List(ctx, src, schema.TableRepos)
	if err != nil {
		return ProjectShowOut{}, err
	}
	// Oldest first, the order `repo list` prints (the store's own order
	// follows the random ids, so two runs would differ).
	slices.SortStableFunc(repos, func(a, b row.Row) int { return a.CreatedAt.Compare(b.CreatedAt) })
	for _, rp := range repos {
		if rp.Deleted || work.Field(rp, fieldProject) != d.ID {
			continue
		}
		d.Repos = append(d.Repos, RepoBrief{ID: rp.ID.String(), MountName: work.Field(rp, fieldMountName),
			DisplayName: work.Field(rp, fieldDisplayName), OriginURL: work.Field(rp, fieldOrigin), ArchivedAt: work.TimeText(rp, fieldArchivedAt)})
	}
	return ProjectShowOut{SchemaVersion: SchemaVersion, Kind: KindProjectShow, Data: d}, nil
}

// projectName is the label of a project in the write lines.
func projectName(r row.Row) string { return work.Field(r, fieldName) }

func (o ops) projectArchive(ctx context.Context, in IDIn) (work.WriteOut, error) {
	return o.stamp(ctx, NounProject, ids.PrefixProject, schema.TableProjects, in.ID, true, projectName)
}

func (o ops) projectUnarchive(ctx context.Context, in IDIn) (work.WriteOut, error) {
	return o.stamp(ctx, NounProject, ids.PrefixProject, schema.TableProjects, in.ID, false, projectName)
}

func (o ops) projectDelete(ctx context.Context, in IDIn) (work.WriteOut, error) {
	return o.remove(ctx, NounProject, ids.PrefixProject, schema.TableProjects, in.ID, projectName)
}

// repoStore is where repos are written: the repo store (repos belong to
// the repo's own project).
const repoStore = store.SourceRepo
