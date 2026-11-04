package infra

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// RepoAddIn is the input of repo.add (v0.1.17 flags --origin,
// --display-name; --project names the project).
type RepoAddIn struct {
	MountName   string `json:"mount_name" arg:"0" desc:"the repo's mount name (a-z, 0-9, - _ .)"`
	Origin      string `json:"origin,omitempty" flag:"origin" desc:"git remote URL"`
	DisplayName string `json:"display_name,omitempty" flag:"display-name" desc:"free-form display name"`
	Project     string `json:"project,omitempty" flag:"project" desc:"override project (id or name)"`
}

// RepoListIn is the input of repo.list.
type RepoListIn struct {
	Project string `json:"project,omitempty" flag:"project" desc:"override project (id or name)"`
}

// Repo is one repo in v0.1.17's --json list form (the ent Repo struct).
type Repo struct {
	ID          string `json:"id"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	ProjectID   string `json:"project_id"`
	MountName   string `json:"mount_name"`
	DisplayName string `json:"display_name,omitempty"`
	OriginURL   string `json:"origin_url,omitempty"`
	ArchivedAt  string `json:"archived_at,omitempty"`
	// line is the v0.1.17 human line of this repo.
	line string
}

// RepoListOut is v0.1.17's list envelope.
type RepoListOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Count         int    `json:"count,omitempty"`
	Data          []Repo `json:"data"`
}

// Text is v0.1.17's list: "<id>  <mount>[  <origin>][ [archived]]".
func (o RepoListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no repos)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		b.WriteString(r.line)
	}
	return b.String()
}

// mountName validates a mount name like v0.1.17's
// textnorm.ValidateIdentifier: trimmed, then only lowercase letters,
// digits, '-', '_' and '.'. An upper-case letter is refused, never folded:
// v0.1.17 answered `repo add Web` with E_INVALID_IDENTIFIER, and folding it
// would silently register (or collide with) a different name.
func mountName(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	ok := s != ""
	for _, r := range s {
		if !identChar(r) {
			ok = false
			break
		}
	}
	if !ok {
		return "", errs.New(errs.InvalidIdentifier, fmt.Sprintf("mount_name %q failed validation", raw)).
			WithHint("use lowercase letters, digits, '-', '_' or '.'")
	}
	return s, nil
}

// identChar is one character v0.1.17's isIdentChar allowed in an
// identifier: a-z, 0-9, '-', '_' and '.'.
func identChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
}

// projectRepos is the live repos of project pid in the repo store.
func (o ops) projectRepos(ctx context.Context, pid string) ([]row.Row, error) {
	rows, err := o.env.List(ctx, repoStore, schema.TableRepos)
	if err != nil {
		return nil, err
	}
	var out []row.Row
	for _, r := range rows {
		if !r.Deleted && work.Field(r, fieldProject) == pid {
			out = append(out, r)
		}
	}
	return out, nil
}

// repoAdd registers a repo in the project (a taken mount name is
// E_MOUNT_NAME_TAKEN, v0.1.17's text).
func (o ops) repoAdd(ctx context.Context, in RepoAddIn) (work.WriteOut, error) {
	name, err := mountName(in.MountName)
	if err != nil {
		return work.WriteOut{}, err
	}
	pid, err := work.Project(ctx, o.env, NounRepo, in.Project)
	if err != nil {
		return work.WriteOut{}, err
	}
	have, err := o.projectRepos(ctx, pid.String())
	if err != nil {
		return work.WriteOut{}, err
	}
	for _, r := range have {
		if work.Field(r, fieldMountName) == name {
			return work.WriteOut{}, errs.New(errs.MountNameTaken, fmt.Sprintf("mount_name %q already exists in this project", name))
		}
	}
	fields, err := work.Texts(NounRepo, map[string]string{fieldMountName: name, fieldOrigin: in.Origin, fieldDisplayName: in.DisplayName})
	if err != nil {
		return work.WriteOut{}, err
	}
	if fields[fieldProject], err = value.RefOf(pid.Any()); err != nil {
		return work.WriteOut{}, err
	}
	w, err := o.env.Create(ctx, repoStore, schema.TableRepos, fields)
	if err != nil {
		return work.WriteOut{}, err
	}
	return work.Out(repoStore, w, fmt.Sprintf("%s repo %s registered (%s)\n", work.Success, name, w.Row.ID)), nil
}

// repoList lists the repos of the project, oldest first.
func (o ops) repoList(ctx context.Context, in RepoListIn) (RepoListOut, error) {
	pid, err := work.Project(ctx, o.env, NounRepo, in.Project)
	if err != nil {
		return RepoListOut{}, err
	}
	rows, err := o.projectRepos(ctx, pid.String())
	if err != nil {
		return RepoListOut{}, err
	}
	sourced := make([]work.Sourced, 0, len(rows))
	for _, r := range rows {
		sourced = append(sourced, work.Sourced{Source: repoStore, Row: r})
	}
	out := RepoListOut{SchemaVersion: SchemaVersion, Kind: KindRepoList, Data: []Repo{}}
	for _, s := range sortByCreated(sourced) {
		r := s.Row
		out.Data = append(out.Data, Repo{
			ID: r.ID.String(), CreatedAt: timeOf(r, true), UpdatedAt: timeOf(r, false), ProjectID: work.Field(r, fieldProject),
			MountName: work.Field(r, fieldMountName), DisplayName: work.Field(r, fieldDisplayName), OriginURL: work.Field(r, fieldOrigin),
			ArchivedAt: work.TimeText(r, fieldArchivedAt), line: listLine(r, work.Field(r, fieldMountName)),
		})
	}
	out.Count = len(out.Data)
	return out, nil
}

// repoName is the label of a repo in the write lines.
func repoName(r row.Row) string { return work.Field(r, fieldMountName) }

func (o ops) repoArchive(ctx context.Context, in IDIn) (work.WriteOut, error) {
	return o.stamp(ctx, NounRepo, ids.PrefixRepo, schema.TableRepos, in.ID, true, repoName)
}

func (o ops) repoUnarchive(ctx context.Context, in IDIn) (work.WriteOut, error) {
	return o.stamp(ctx, NounRepo, ids.PrefixRepo, schema.TableRepos, in.ID, false, repoName)
}

func (o ops) repoDelete(ctx context.Context, in IDIn) (work.WriteOut, error) {
	return o.remove(ctx, NounRepo, ids.PrefixRepo, schema.TableRepos, in.ID, repoName)
}
