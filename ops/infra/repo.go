package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/store"

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

func (o ops) repoDelete(ctx context.Context, in DeleteIn) (work.WriteOut, error) {
	return o.remove(engine.WithDeleteReason(ctx, in.Reason), NounRepo, ids.PrefixRepo, schema.TableRepos, in.ID, repoName)
}

// ---- Repo identity and URL history (PROV1, PROV2, PROV3) ----

// Operation names of the repo identity verbs.
const (
	OpRepoSetURL = "repo.set_url"
	OpRepoFork   = "repo.fork"
)

// Field names of the repo identity (schema coreDefs, repos table).
const (
	fieldURLs       = "urls"
	fieldForkedFrom = "forked_from"
	fieldSplitFrom  = "split_from"
)

// dateLayout is the date form of a URL history entry.
const dateLayout = "2006-01-02"

// originRemote is the remote whose address a new identity records first
// (git's default remote name), before every other remote.
const originRemote = "origin"

// forkSuffix makes a fork's mount name distinct from its upstream's (the
// repos table is unique on project_id + mount_name, and both rows live in
// the fork's repo store).
const forkSuffix = "-fork"

// Remote is one configured git remote URL, exactly as written in git
// config (never `git remote get-url`, which applies insteadOf rewrites).
type Remote struct {
	Name string
	URL  string
}

// RepoFiles is what the repo identity verbs need beyond Env: the
// checkout's _meta.json, its git remotes, its folder name and the acting
// actor. wire.App implements it; an Env without it simply has no `repo
// set-url` / `repo fork` (an embedder without a checkout).
type RepoFiles interface {
	// ReadRepoMeta reads the identity keys of .lore/data/_meta.json;
	// exists is false when the file is missing.
	ReadRepoMeta(ctx context.Context) (m rowfs.RepoMeta, exists bool, err error)
	// WriteRepoMeta sets the identity keys, keeping every other key.
	WriteRepoMeta(ctx context.Context, m rowfs.RepoMeta) error
	// RepoRemotes is every remote.<name>.url, in config order.
	RepoRemotes(ctx context.Context) ([]Remote, error)
	// RepoDirName is the base name of the checkout's top folder.
	RepoDirName(ctx context.Context) (string, error)
	// RepoActor is the acting actor's id ("" when unknown).
	RepoActor(ctx context.Context) string
	// CreateWithID creates a row with a chosen id through the App's write
	// path: a repos row's id IS the repo_id of _meta.json (PROV1).
	CreateWithID(ctx context.Context, target store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) (engine.Written, error)
}

// Identity is a checkout's repo identity: the _meta.json keys and the
// synced repos row with its URL history.
type Identity struct {
	Meta    rowfs.RepoMeta
	Row     row.Row
	History repourl.History
}

// URLWarning is the credential warning of one remote (W_URL_CREDENTIALS):
// the stored, clean form only, never the credential (URL CREDENTIALS FIX).
type URLWarning struct {
	Code   string `json:"code"`
	Remote string `json:"remote,omitempty"`
	Stored string `json:"stored"`
}

// WarnURLUserinfo is the warning code of a URL that held a credential
// (named after errs.URLUserinfo, the matching refusal).
const WarnURLUserinfo = "W_URL_CREDENTIALS"

// AddressNotice is one unconfirmed address of this clone (PROV2).
type AddressNotice struct {
	// URL is the clean address the clone uses; Key its normalised form.
	URL string `json:"url"`
	Key string `json:"key"`
	// Remote is the git remote name ("" when it no longer is a remote).
	Remote string `json:"remote,omitempty"`
	// Recorded is the repo's confirmed primary key ("" when none).
	Recorded string `json:"recorded,omitempty"`
	// CaseOnly is PROV3: the address differs from a confirmed one only in
	// letter case, so it is the same repo and `fork` is never offered.
	CaseOnly bool `json:"case_only,omitempty"`
	// New is true on the run that first recorded it (Q2: the only run that
	// may ask in a terminal).
	New bool `json:"new,omitempty"`
}

// AddressReport is the result of CheckAddresses.
type AddressReport struct {
	RepoID   string          `json:"repo_id"`
	Minted   bool            `json:"minted,omitempty"`
	Notices  []AddressNotice `json:"notices,omitempty"`
	Warnings []URLWarning    `json:"warnings,omitempty"`
	// Mismatch is Q5's doctor finding: this clone's remote is a confirmed
	// address of ANOTHER repo row (typically a fork whose _meta.json was
	// replaced by its upstream's in a rebase); "" when none.
	Mismatch string `json:"mismatch,omitempty"`
}

// today is the env clock's date.
func today(env Env) string { return opclock.Now(env).UTC().Format(dateLayout) }

// cleanRemotes is every remote URL that names a repo, stripped of
// credentials (one warning per remote that held one), origin first.
func cleanRemotes(rs []Remote) (out []Remote, warns []URLWarning) {
	seen := map[string]bool{}
	ordered := slices.Clone(rs)
	slices.SortStableFunc(ordered, func(a, b Remote) int {
		switch {
		case a.Name == originRemote && b.Name != originRemote:
			return -1
		case b.Name == originRemote && a.Name != originRemote:
			return 1
		}
		return 0
	})
	for _, r := range ordered {
		clean, stripped, err := repourl.StripCredentials(r.URL)
		if err != nil {
			continue
		}
		key, err := repourl.Normalize(clean)
		if err != nil || seen[key] {
			continue // a local path cannot identify a repo
		}
		seen[key] = true
		if stripped {
			warns = append(warns, URLWarning{Code: WarnURLUserinfo, Remote: r.Name, Stored: clean})
		}
		out = append(out, Remote{Name: r.Name, URL: clean})
	}
	return out, warns
}

// historyOf decodes a repos row's urls field. A JSON field holding an
// array reads back from a row file as a value.List of JSON items, so the
// field is decoded from its JSON form whatever its kind.
func historyOf(r row.Row) (repourl.History, error) {
	v, ok := r.Fields[fieldURLs]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return nil, nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "infra: read the repo's urls", err)
	}
	return repourl.ParseHistory(raw)
}

// historyValue encodes h for the urls field.
func historyValue(h repourl.History) (value.Value, error) {
	b, err := h.JSON()
	if err != nil {
		return nil, err
	}
	return value.JSONOf(b)
}

// metaFor is _meta.json's identity after the history changed: the
// confirmed list follows the history (newest first); the id and lineage
// stay.
func metaFor(m rowfs.RepoMeta, h repourl.History) rowfs.RepoMeta {
	m.URLs = h.Confirmed()
	return m
}

// EnsureIdentity gives the checkout its repo identity when it has none
// yet, and returns it (PROV1, PROVENANCE_DESIGN 10.1): `lore init` and the
// first 0.1.18 sync call it. minted reports a new id.
//
// The id is random and permanent, never derived from a name or a URL. A
// v0.1.17 repos row that already describes this checkout (its origin_url
// is this clone's remote, or its mount name is the folder's name) is
// adopted, so a registered repo keeps its id. A checkout without
// _meta.json (no `lore init` yet) has no identity and gets none: E_NOT_FOUND.
func EnsureIdentity(ctx context.Context, env Env, files RepoFiles) (id Identity, minted bool, err error) {
	meta, exists, err := files.ReadRepoMeta(ctx)
	if err != nil {
		return Identity{}, false, err
	}
	if !exists {
		return Identity{}, false, errs.New(errs.NotFound, "this checkout has no .lore/data/_meta.json").
			WithHint("run `lore init` in the repository first")
	}
	pid, err := env.Project(ctx)
	if err != nil {
		return Identity{}, false, err
	}
	rows, err := o(env).projectRepos(ctx, pid.String())
	if err != nil {
		return Identity{}, false, err
	}
	remotes, err := files.RepoRemotes(ctx)
	if err != nil {
		return Identity{}, false, err
	}
	clean, _ := cleanRemotes(remotes)
	if meta.RepoID != "" {
		for _, r := range rows {
			if r.ID.String() == meta.RepoID {
				h, herr := historyOf(r)
				return Identity{Meta: meta, Row: r, History: h}, false, herr
			}
		}
		// The id is committed but its row is not here (a hand edit, or a
		// checkout whose row file was lost): the row is rebuilt from the
		// confirmed list, which is all a clone knows.
		h := migrateHistory(meta.URLs, today(env), files.RepoActor(ctx))
		r, cerr := createRepoRow(ctx, files, pid, meta.RepoID, rows, h, meta)
		return Identity{Meta: meta, Row: r, History: h}, false, cerr
	}
	adopted, ok := adoptRow(ctx, files, rows, clean)
	h := repourl.History{}
	by, day := files.RepoActor(ctx), today(env)
	if ok {
		if h, err = historyOf(adopted); err != nil {
			return Identity{}, false, err
		}
		if len(h) == 0 {
			if o := work.Field(adopted, fieldOrigin); o != "" {
				if c, _, serr := repourl.StripCredentials(o); serr == nil {
					h = repourl.History{{URL: c, Kind: repourl.KindPrimary, How: repourl.HowMigrate, From: day, By: by}}
				}
			}
		}
	}
	if len(h) == 0 && len(clean) > 0 {
		h = repourl.History{{URL: clean[0].URL, Kind: repourl.KindPrimary, How: repourl.HowInit, From: day, By: by}}
	}
	meta.URLs = h.Confirmed()
	if ok {
		meta.RepoID = adopted.ID.String()
		hv, herr := historyValue(h)
		if herr != nil {
			return Identity{}, false, herr
		}
		w, uerr := env.Update(ctx, engine.Ref{Store: repoStore, Table: schema.TableRepos, ID: adopted.ID}, map[string]value.Value{fieldURLs: hv})
		if uerr != nil {
			return Identity{}, false, uerr
		}
		if werr := files.WriteRepoMeta(ctx, meta); werr != nil {
			return Identity{}, false, werr
		}
		return Identity{Meta: meta, Row: w.Row, History: h}, true, nil
	}
	nid, err := ids.New[ids.Repo]()
	if err != nil {
		return Identity{}, false, errs.Wrap(errs.Internal, "infra: mint a repo id", err)
	}
	meta.RepoID = nid.String()
	r, err := createRepoRow(ctx, files, pid, meta.RepoID, rows, h, meta)
	if err != nil {
		return Identity{}, false, err
	}
	if werr := files.WriteRepoMeta(ctx, meta); werr != nil {
		return Identity{}, false, werr
	}
	return Identity{Meta: meta, Row: r, History: h}, true, nil
}

// o is the ops value of env (the helpers hang off it).
func o(env Env) ops { return ops{env: env} }

// migrateHistory is a history rebuilt from _meta.json's confirmed list:
// the first entry primary, the rest mirrors (how "migrate").
func migrateHistory(urls []string, day, by string) repourl.History {
	var h repourl.History
	for i, u := range urls {
		k := repourl.KindMirror
		if i == 0 {
			k = repourl.KindPrimary
		}
		h = append(h, repourl.Entry{URL: u, Kind: k, How: repourl.HowMigrate, From: day, By: by})
	}
	return h
}

// adoptRow is the live v0.1.17 repos row that already describes this
// checkout: one whose origin_url is a remote of this clone, else one whose
// mount name is the checkout folder's name.
func adoptRow(ctx context.Context, files RepoFiles, rows []row.Row, remotes []Remote) (row.Row, bool) {
	keys := map[string]bool{}
	for _, r := range remotes {
		if k, err := repourl.Normalize(r.URL); err == nil {
			keys[k] = true
		}
	}
	for _, r := range rows {
		if k, err := repourl.Normalize(work.Field(r, fieldOrigin)); err == nil && keys[k] {
			return r, true
		}
	}
	name, err := files.RepoDirName(ctx)
	if err != nil {
		return row.Row{}, false
	}
	for _, r := range rows {
		if work.Field(r, fieldMountName) == name {
			return r, true
		}
	}
	return row.Row{}, false
}

// mountFor is a free mount name for a new repo row: want when it is a
// valid identifier, else "repo"; then want-2, want-3, … while taken.
func mountFor(want string, rows []row.Row) string {
	base := strings.ToLower(strings.TrimSpace(want))
	if _, err := mountName(base); err != nil {
		base = NounRepo
	}
	taken := map[string]bool{}
	for _, r := range rows {
		taken[work.Field(r, fieldMountName)] = true
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = base + "-" + strconv.Itoa(n)
	}
	return name
}

// createRepoRow writes the repos row of id with history h (origin_url is
// the open primary) under a free mount name.
func createRepoRow(ctx context.Context, files RepoFiles, pid ids.ID[ids.Project], id string, rows []row.Row, h repourl.History, meta rowfs.RepoMeta) (row.Row, error) {
	aid, err := ids.ParseAny(id)
	if err != nil {
		return row.Row{}, errs.Wrap(errs.InvalidInput, "infra: invalid repo id", err)
	}
	dir, err := files.RepoDirName(ctx)
	if err != nil {
		return row.Row{}, err
	}
	texts := map[string]string{fieldMountName: mountFor(dir, rows)}
	if p, ok := h.Primary(); ok {
		texts[fieldOrigin] = p.URL
	}
	fields, err := work.Texts(NounRepo, texts)
	if err != nil {
		return row.Row{}, err
	}
	if fields[fieldProject], err = value.RefOf(pid.Any()); err != nil {
		return row.Row{}, err
	}
	if fields[fieldURLs], err = historyValue(h); err != nil {
		return row.Row{}, err
	}
	for field, up := range map[string]string{fieldForkedFrom: meta.ForkedFrom, fieldSplitFrom: meta.SplitFrom} {
		if up == "" {
			continue
		}
		uid, perr := ids.ParseAny(up)
		if perr != nil {
			return row.Row{}, errs.Wrap(errs.InvalidInput, "infra: invalid "+field, perr)
		}
		if fields[field], err = value.RefOf(uid); err != nil {
			return row.Row{}, err
		}
	}
	w, err := files.CreateWithID(ctx, repoStore, schema.TableRepos, aid, fields)
	if err != nil {
		return row.Row{}, err
	}
	return w.Row, nil
}

// CheckAddresses is the sync pass of PROVENANCE_DESIGN 10.3 (PROV2,
// PROV3): it ensures the identity, then compares every remote of this
// clone with the repo's URL history. A known address (any kind, any form
// of it) changes nothing; a new one is appended as `unconfirmed` (New) and
// trusted only after `lore repo set-url`. Every open unconfirmed address
// of this clone is reported on every pass (Q2: the state is the entry
// itself; the notice repeats until it is confirmed or removed). It never
// decides: no address becomes confirmed here, and nothing is asked.
func CheckAddresses(ctx context.Context, env Env, files RepoFiles) (AddressReport, error) {
	id, minted, err := EnsureIdentity(ctx, env, files)
	if err != nil {
		return AddressReport{}, err
	}
	remotes, err := files.RepoRemotes(ctx)
	if err != nil {
		return AddressReport{}, err
	}
	clean, warns := cleanRemotes(remotes)
	rep := AddressReport{RepoID: id.Meta.RepoID, Minted: minted, Warnings: warns}
	h, fresh := id.History, map[string]bool{}
	for _, r := range clean {
		next, added, serr := h.Seen(r.URL, today(env), files.RepoActor(ctx))
		if serr != nil {
			continue
		}
		if added {
			k, _ := repourl.Normalize(r.URL)
			fresh[k] = true
		}
		h = next
	}
	if len(fresh) > 0 {
		hv, herr := historyValue(h)
		if herr != nil {
			return AddressReport{}, herr
		}
		if _, uerr := env.Update(ctx, engine.Ref{Store: repoStore, Table: schema.TableRepos, ID: id.Row.ID}, map[string]value.Value{fieldURLs: hv}); uerr != nil {
			return AddressReport{}, uerr
		}
	}
	rep.Notices = notices(h, clean, fresh)
	return rep, nil
}

// notices is one AddressNotice per open unconfirmed address of h that this
// clone uses (an unconfirmed address another clone recorded is listed by
// AddressStatus, not here: this clone cannot answer for it).
func notices(h repourl.History, clean []Remote, fresh map[string]bool) []AddressNotice {
	recorded := ""
	if p, ok := h.Primary(); ok {
		recorded = p.Key()
	}
	byKey := map[string]string{}
	for _, r := range clean {
		if k, err := repourl.Normalize(r.URL); err == nil {
			byKey[k] = r.Name
		}
	}
	var out []AddressNotice
	for _, e := range h.Unconfirmed() {
		k := e.Key()
		name, ours := byKey[k]
		if !ours {
			continue
		}
		_, caseOnly := h.CaseVariant(k)
		out = append(out, AddressNotice{URL: e.URL, Key: k, Remote: name, Recorded: recorded, CaseOnly: caseOnly, New: fresh[k]})
	}
	return out
}

// AddressStatus is the read-only view `lore doctor` prints (Repo
// addresses): every open unconfirmed address of the repo (whichever clone
// recorded it), and Q5's mismatch: this clone's remote is a confirmed
// address of another repos row while not one of its own. It writes
// nothing and mints nothing (a checkout without an identity reports
// RepoID "").
func AddressStatus(ctx context.Context, env Env, files RepoFiles) (AddressReport, error) {
	meta, exists, err := files.ReadRepoMeta(ctx)
	if err != nil || !exists || meta.RepoID == "" {
		return AddressReport{}, err
	}
	rep := AddressReport{RepoID: meta.RepoID}
	rows, err := env.List(ctx, repoStore, schema.TableRepos)
	if err != nil {
		return AddressReport{}, err
	}
	remotes, err := files.RepoRemotes(ctx)
	if err != nil {
		return AddressReport{}, err
	}
	clean, warns := cleanRemotes(remotes)
	rep.Warnings = warns
	var own repourl.History
	others := map[string]string{} // confirmed key -> repo id of another row
	for _, r := range rows {
		if r.Deleted {
			continue
		}
		h, herr := historyOf(r)
		if herr != nil {
			continue
		}
		if r.ID.String() == meta.RepoID {
			own = h
			continue
		}
		for _, k := range h.Confirmed() {
			others[k] = r.ID.String()
		}
	}
	mine := slices.Clone(meta.URLs)
	mine = append(mine, own.Confirmed()...)
	for _, r := range clean {
		k, nerr := repourl.Normalize(r.URL)
		if nerr != nil || slices.Contains(mine, k) {
			continue
		}
		if other, ok := others[k]; ok && rep.Mismatch == "" {
			rep.Mismatch = "this clone's remote " + k + " is a confirmed address of " + other + ", but .lore/data/_meta.json says " + meta.RepoID
		}
	}
	recorded := ""
	if p, ok := own.Primary(); ok {
		recorded = p.Key()
	}
	byKey := map[string]string{}
	for _, r := range clean {
		if k, nerr := repourl.Normalize(r.URL); nerr == nil {
			byKey[k] = r.Name
		}
	}
	for _, e := range own.Unconfirmed() {
		k := e.Key()
		_, caseOnly := own.CaseVariant(k)
		rep.Notices = append(rep.Notices, AddressNotice{URL: e.URL, Key: k, Remote: byKey[k], Recorded: recorded, CaseOnly: caseOnly})
	}
	return rep, nil
}

// RepoSetURLIn is the input of `lore repo set-url` (PROVENANCE_DESIGN 10.4).
type RepoSetURLIn struct {
	URL    string `json:"url" arg:"0" desc:"the repo's address (https, ssh or host/owner/repo)"`
	Mirror bool   `json:"mirror,omitempty" flag:"mirror" desc:"add the address as a mirror (the primary is unchanged)"`
	Remove bool   `json:"remove,omitempty" flag:"remove" desc:"close a mirror or drop an unconfirmed address"`
	From   string `json:"from,omitempty" flag:"from" desc:"start date YYYY-MM-DD of the entry (default today)"`
	// Repo names another repo of this project by mount name (or rep_ id):
	// its row is updated; its own checkout's _meta.json is not touched from here
	// (CLI_REFERENCE 3.14).
	Repo string `json:"repo,omitempty" flag:"repo" desc:"the repo of this project to update, by mount name or rep_ id (default this checkout)"`
}

// RepoSetURLOut is the result of `lore repo set-url`.
type RepoSetURLOut struct {
	RepoID string `json:"repo_id"`
	URL    string `json:"url"`
	Key    string `json:"key"`
	// Mode is primary, mirror or remove.
	Mode string `json:"mode"`
	// Changed is false when the history already said so.
	Changed bool `json:"changed"`
	// Previous is the primary key this one replaced ("" when none).
	Previous string `json:"previous,omitempty"`
	// Confirmed is the confirmed list now in _meta.json, newest first.
	Confirmed []string     `json:"confirmed"`
	Warnings  []URLWarning `json:"warnings,omitempty"`
	human     string
	// other is set for --repo naming a repo other than this checkout.
	other bool
}

// Text implements op.Texter.
func (o RepoSetURLOut) Text() string { return o.human }

// The set-url modes as printed (closed set).
const (
	modePrimary = "primary"
	modeMirror  = "mirror"
	modeRemove  = "remove"
)

// repoSetURL confirms an address (PROV2): the only way an `unconfirmed`
// entry becomes trusted. An agent may run it itself (AGENTS OVERRIDE); the
// audit fields record who did.
func repoSetURL(ctx context.Context, env Env, files RepoFiles, in RepoSetURLIn) (RepoSetURLOut, error) {
	if in.Mirror && in.Remove {
		return RepoSetURLOut{}, errs.New(errs.InvalidInput, "--mirror and --remove cannot be used together")
	}
	clean, stripped, err := repourl.StripCredentials(in.URL)
	if err != nil {
		return RepoSetURLOut{}, errs.New(errs.URLUserinfo, "the URL cannot be read, and lore cannot tell whether it holds a credential").
			WithHint("give the address without any user name or token, e.g. https://github.com/<owner>/<repo>.git")
	}
	key, err := repourl.Normalize(clean)
	if err != nil {
		// E_REPO_URL: the address does not normalise (PROVENANCE 10.4)
		return RepoSetURLOut{}, errs.Wrap(errs.RepoURL, "not a repo address: "+clean, err).
			WithHint("use https://host/owner/repo(.git), git@host:owner/repo.git or host/owner/repo")
	}
	day := today(env)
	from := day
	if strings.TrimSpace(in.From) != "" {
		t, perr := time.Parse(dateLayout, strings.TrimSpace(in.From))
		if perr != nil {
			return RepoSetURLOut{}, errs.New(errs.InvalidInput, "--from must be a date YYYY-MM-DD")
		}
		from = t.Format(dateLayout)
	}
	id, _, err := EnsureIdentity(ctx, env, files)
	if err != nil {
		return RepoSetURLOut{}, err
	}
	// --repo <mount>: another repo row of the project; this checkout's
	// _meta.json is written only when the row is this checkout's own.
	own := true
	if r := strings.TrimSpace(in.Repo); r != "" {
		if id, own, err = otherRepo(ctx, env, id, r); err != nil {
			return RepoSetURLOut{}, err
		}
	}
	mode, label := repourl.SetPrimary, modePrimary
	switch {
	case in.Mirror:
		mode, label = repourl.SetMirror, modeMirror
	case in.Remove:
		mode, label = repourl.SetRemove, modeRemove
	}
	prev := ""
	if p, ok := id.History.Primary(); ok {
		prev = p.Key()
	}
	h, changed, err := id.History.Set(clean, mode, from, day, files.RepoActor(ctx))
	if err != nil {
		return RepoSetURLOut{}, err
	}
	out := RepoSetURLOut{RepoID: id.Meta.RepoID, URL: clean, Key: key, Mode: label, Changed: changed, other: !own}
	if stripped {
		out.Warnings = []URLWarning{{Code: WarnURLUserinfo, Stored: clean}}
	}
	if changed {
		patch := map[string]value.Value{}
		if patch[fieldURLs], err = historyValue(h); err != nil {
			return RepoSetURLOut{}, err
		}
		if p, ok := h.Primary(); ok && mode == repourl.SetPrimary {
			if patch[fieldOrigin], err = value.TextOf(p.URL); err != nil {
				return RepoSetURLOut{}, err
			}
		}
		if _, err = env.Update(ctx, engine.Ref{Store: repoStore, Table: schema.TableRepos, ID: id.Row.ID}, patch); err != nil {
			return RepoSetURLOut{}, err
		}
		if own {
			if err = files.WriteRepoMeta(ctx, metaFor(id.Meta, h)); err != nil {
				return RepoSetURLOut{}, err
			}
		}
	}
	out.Confirmed = h.Confirmed()
	if mode == repourl.SetPrimary && prev != key {
		out.Previous = prev
	}
	out.human = setURLText(out)
	return out, nil
}

// otherRepo resolves `set-url --repo <name>` to a repo row of this
// project by mount name or rep_ id; own reports whether it is this
// checkout's row (then its _meta.json is written too). An unknown name is
// E_NOT_FOUND naming lore repo list.
func otherRepo(ctx context.Context, env Env, self Identity, name string) (Identity, bool, error) {
	pid, err := env.Project(ctx)
	if err != nil {
		return Identity{}, false, err
	}
	rows, err := o(env).projectRepos(ctx, pid.String())
	if err != nil {
		return Identity{}, false, err
	}
	for _, r := range rows {
		if r.ID.String() != name && work.Field(r, fieldMountName) != name {
			continue
		}
		if r.ID == self.Row.ID {
			return self, true, nil
		}
		h, herr := historyOf(r)
		return Identity{Row: r, History: h, Meta: rowfs.RepoMeta{RepoID: r.ID.String()}}, false, herr
	}
	return Identity{}, false, errs.New(errs.NotFound, "repo set-url: no repo "+strconv.Quote(name)+" in this project").
		WithHint("lore repo list shows the mount names")
}

// setURLText is the human output of `lore repo set-url` (10.4).
func setURLText(o RepoSetURLOut) string {
	var b strings.Builder
	for _, w := range o.Warnings {
		b.WriteString("warning: a credential was in the URL you gave; lore stored it without it: " + w.Stored + "\n")
		b.WriteString("         if this URL is in a git remote too, remove it there: git remote set-url <remote> " + w.Stored + "\n")
	}
	switch {
	case !o.Changed:
		b.WriteString(work.Success + " " + o.RepoID + " already has " + o.Key + " as " + o.Mode + "; nothing changed\n")
	case o.Mode == modeMirror:
		b.WriteString(work.Success + " " + o.RepoID + " mirror address added: " + o.Key + "\n")
	case o.Mode == modeRemove:
		b.WriteString(work.Success + " " + o.RepoID + " address removed: " + o.Key + "\n")
	case o.Previous != "":
		b.WriteString(work.Success + " " + o.RepoID + " primary address is now " + o.Key + " (was " + o.Previous + " until today)\n")
	default:
		b.WriteString(work.Success + " " + o.RepoID + " primary address is now " + o.Key + "\n")
	}
	switch {
	case o.Changed && o.other:
		b.WriteString("  the repo row is updated; that checkout's .lore/data/_meta.json is not changed from here (run set-url there to update it)\n")
	case o.Changed:
		b.WriteString("  .lore/data/_meta.json updated; commit it with your next push\n")
	}
	return b.String()
}

// RepoForkIn is the input of `lore repo fork` (PROVENANCE_DESIGN 10.6,
// 11.5).
type RepoForkIn struct {
	Split bool `json:"split,omitempty" flag:"split" desc:"this repo was split from the recorded one (records split_from instead of forked_from)"`
}

// RepoForkOut is the result of `lore repo fork`.
type RepoForkOut struct {
	RepoID     string `json:"repo_id"`
	ForkedFrom string `json:"forked_from,omitempty"`
	SplitFrom  string `json:"split_from,omitempty"`
	MountName  string `json:"mount_name"`
	URL        string `json:"url,omitempty"`
	human      string
}

// Text implements op.Texter.
func (o RepoForkOut) Text() string { return o.human }

// repoFork gives this clone its own permanent repo id (PROV1: a fork is a
// new repo). The new id and forked_from (or split_from) go into this
// clone's _meta.json with the clone's remote as the only confirmed
// address; a new repos row records the same. The project id is kept.
// Rows made before the fork keep their upstream `_o.repo` (they were made
// there); new rows get the fork's id.
func repoFork(ctx context.Context, env Env, files RepoFiles, in RepoForkIn) (RepoForkOut, error) {
	up, _, err := EnsureIdentity(ctx, env, files)
	if err != nil {
		return RepoForkOut{}, err
	}
	remotes, err := files.RepoRemotes(ctx)
	if err != nil {
		return RepoForkOut{}, err
	}
	clean, _ := cleanRemotes(remotes)
	if len(clean) == 0 {
		return RepoForkOut{}, errs.New(errs.InvalidInput, "this clone has no git remote, so it cannot be told apart from "+up.Meta.RepoID).
			WithHint("add the fork's remote first: git remote add origin <url>")
	}
	if slices.Contains(up.History.Confirmed(), mustKey(clean[0].URL)) {
		return RepoForkOut{}, errs.New(errs.InvalidInput, "this clone's remote "+mustKey(clean[0].URL)+" is a confirmed address of "+up.Meta.RepoID+", so it is that repo, not a fork").
			WithHint("change the remote to the fork's address first (git remote set-url origin <fork url>)")
	}
	if _, caseOnly := up.History.CaseVariant(mustKey(clean[0].URL)); caseOnly {
		// PROV3: a case-only difference is the same repo; never a fork.
		return RepoForkOut{}, errs.New(errs.InvalidInput, "this clone's remote differs from "+up.Meta.RepoID+"'s address only in letter case: it is the same repo").
			WithHint("confirm it with `lore repo set-url " + mustKey(clean[0].URL) + "`")
	}
	nid, err := ids.New[ids.Repo]()
	if err != nil {
		return RepoForkOut{}, errs.Wrap(errs.Internal, "infra: mint a repo id", err)
	}
	h := repourl.History{{URL: clean[0].URL, Kind: repourl.KindPrimary, How: repourl.HowInit, From: today(env), By: files.RepoActor(ctx)}}
	meta := rowfs.RepoMeta{RepoID: nid.String()}
	if in.Split {
		meta.SplitFrom = up.Meta.RepoID
	} else {
		meta.ForkedFrom = up.Meta.RepoID
	}
	meta = metaFor(meta, h)
	pid, err := env.Project(ctx)
	if err != nil {
		return RepoForkOut{}, err
	}
	rows, err := o(env).projectRepos(ctx, pid.String())
	if err != nil {
		return RepoForkOut{}, err
	}
	r, err := createRepoRow(ctx, forkNamed{RepoFiles: files, name: work.Field(up.Row, fieldMountName) + forkSuffix}, pid, meta.RepoID, rows, h, meta)
	if err != nil {
		return RepoForkOut{}, err
	}
	if err := files.WriteRepoMeta(ctx, meta); err != nil {
		return RepoForkOut{}, err
	}
	out := RepoForkOut{RepoID: meta.RepoID, ForkedFrom: meta.ForkedFrom, SplitFrom: meta.SplitFrom, MountName: work.Field(r, fieldMountName), URL: clean[0].URL}
	rel := "forked_from " + meta.ForkedFrom
	if in.Split {
		rel = "split_from " + meta.SplitFrom
	}
	out.human = work.Success + " new repo " + out.RepoID + " (" + out.MountName + "), " + rel + "\n" +
		work.Success + " .lore/data/_meta.json updated; commit it with your next push\n"
	return out, nil
}

// forkNamed is files with the fork's mount name in place of the folder
// name (createRepoRow names a row after RepoDirName).
type forkNamed struct {
	RepoFiles
	name string
}

// RepoDirName implements RepoFiles.
func (f forkNamed) RepoDirName(context.Context) (string, error) { return f.name, nil }

// mustKey is the normalised key of an address cleanRemotes already
// normalised once (so it cannot fail).
func mustKey(clean string) string {
	k, _ := repourl.Normalize(clean)
	return k
}

// OpRepoMergeIDs is `lore repo merge-ids` (PROVENANCE_DESIGN 11.9).
const OpRepoMergeIDs = "repo.merge_ids"

// mergeIDsShown bounds how many rows the confirmation question names.
const mergeIDsShown = 20

// RepoMergeIDsIn is the input of `lore repo merge-ids`.
type RepoMergeIDsIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"list the rows that would be rewritten; write nothing"`
}

// MergedRow is one row whose _o.repo was (or would be) rewritten.
type MergedRow struct {
	Store string `json:"store"`
	Table string `json:"table"`
	ID    string `json:"id"`
	From  string `json:"from"`
}

// RepoMergeIDsOut is the result of `lore repo merge-ids`.
type RepoMergeIDsOut struct {
	RepoID  string      `json:"repo_id"`
	Aliases []string    `json:"aliases"`
	Rows    []MergedRow `json:"rows"`
	DryRun  bool        `json:"dry_run,omitempty"`
	human   string
}

// Text implements op.Texter.
func (o RepoMergeIDsOut) Text() string { return o.human }

// OriginRewriter is the one write that may change a row's `_o` after
// creation (engine.RewriteOrigin behind the App's write path).
type OriginRewriter interface {
	RewriteOrigin(ctx context.Context, ref engine.Ref, fn func(value.JSON) (value.JSON, bool, error)) (engine.Written, error)
}

// repoMergeIDs settles the race of 11.9 for good: every live row whose
// `_o.repo` is one of _meta.json's repo_id_aliases gets the winning id, and
// the aliases are cleared. It is the only command that rewrites `_o`
// (5.3); the audit log records the op, the rows record nothing new.
//
// Flow: identity → scan every store's synced tables for alias-stamped rows
// → --dry-run: list → ask (E_NEEDS_CONFIRM unless --yes) → RewriteOrigin
// each → clear aliases in _meta.json.
func repoMergeIDs(ctx context.Context, env Env, files RepoFiles, rw OriginRewriter, in RepoMergeIDsIn) (RepoMergeIDsOut, error) {
	id, _, err := EnsureIdentity(ctx, env, files)
	if err != nil {
		return RepoMergeIDsOut{}, err
	}
	out := RepoMergeIDsOut{RepoID: id.Meta.RepoID, Aliases: slices.Clone(id.Meta.Aliases), Rows: []MergedRow{}, DryRun: in.DryRun}
	if out.Aliases == nil {
		out.Aliases = []string{}
	}
	if len(out.Aliases) == 0 {
		out.human = work.Success + " " + out.RepoID + " has no alias ids; nothing to merge\n"
		return out, nil
	}
	alias := map[string]bool{}
	for _, a := range out.Aliases {
		alias[a] = true
	}
	for _, src := range env.Stores() {
		for _, def := range schema.Builtin() {
			if !def.Synced {
				continue
			}
			rows, lerr := env.List(ctx, src, def.Name)
			if lerr != nil {
				return RepoMergeIDsOut{}, lerr
			}
			for _, r := range rows {
				if from := originRepo(r.Origin); alias[from] {
					out.Rows = append(out.Rows, MergedRow{Store: string(src), Table: r.Table.String(), ID: r.ID.String(), From: from})
				}
			}
		}
	}
	if in.DryRun {
		out.human = mergeIDsText(out, "would rewrite")
		return out, nil
	}
	if !engine.Confirmed(ctx) {
		return RepoMergeIDsOut{}, errs.New(errs.NeedsConfirm, mergeIDsQuestion(out)).
			WithHint("check the rows (lore repo merge-ids --dry-run), then rerun with --yes")
	}
	winner := canon.String(out.RepoID)
	for _, m := range out.Rows {
		rid, perr := ids.ParseAny(m.ID)
		if perr != nil {
			return RepoMergeIDsOut{}, errs.Wrap(errs.Internal, "infra: a listed row id does not parse", perr)
		}
		ref := engine.Ref{Store: store.Source(m.Store), Table: row.Table(m.Table), ID: rid}
		if _, werr := rw.RewriteOrigin(ctx, ref, func(o value.JSON) (value.JSON, bool, error) {
			return replaceOriginRepo(o, alias, winner)
		}); werr != nil {
			return RepoMergeIDsOut{}, werr
		}
	}
	meta := id.Meta
	meta.Aliases = nil
	if err := files.WriteRepoMeta(ctx, meta); err != nil {
		return RepoMergeIDsOut{}, err
	}
	out.human = mergeIDsText(out, "rewrote")
	return out, nil
}

// originRepo is `_o.repo` of a row ("" when absent).
func originRepo(o value.JSON) string {
	if o.IsZero() {
		return ""
	}
	ms, err := canon.Members(o.Raw())
	if err != nil {
		return ""
	}
	for _, m := range ms {
		if m.Key == origin.KeyRepo {
			var s string
			if json.Unmarshal(m.Raw, &s) == nil {
				return s
			}
		}
	}
	return ""
}

// replaceOriginRepo sets `_o.repo` to winner when it is an alias, keeping
// every other key byte for byte.
func replaceOriginRepo(o value.JSON, alias map[string]bool, winner []byte) (value.JSON, bool, error) {
	if !alias[originRepo(o)] {
		return o, false, nil
	}
	ms, err := canon.Members(o.Raw())
	if err != nil {
		return value.JSON{}, false, errs.Wrap(errs.InvalidInput, "infra: _o is not an object", err)
	}
	for i := range ms {
		if ms[i].Key == origin.KeyRepo {
			ms[i].Raw = winner
		}
	}
	obj, err := canon.Object(ms)
	if err != nil {
		return value.JSON{}, false, errs.Wrap(errs.Internal, "infra: build _o", err)
	}
	j, err := value.JSONOf(obj)
	return j, err == nil, err
}

// mergeIDsQuestion is the E_NEEDS_CONFIRM message: what changes, the first
// rows by id.
func mergeIDsQuestion(o RepoMergeIDsOut) string {
	parts := make([]string, 0, mergeIDsShown)
	for i, r := range o.Rows {
		if i == mergeIDsShown {
			parts = append(parts, "and "+strconv.Itoa(len(o.Rows)-mergeIDsShown)+" more")
			break
		}
		parts = append(parts, r.ID)
	}
	msg := "repo merge-ids rewrites _o.repo of " + strconv.Itoa(len(o.Rows)) + " rows from " + strings.Join(o.Aliases, ", ") + " to " + o.RepoID + " and clears the aliases"
	if len(parts) > 0 {
		msg += ": " + strings.Join(parts, ", ")
	}
	return msg
}

// mergeIDsText is the human output (verb: "would rewrite" or "rewrote").
func mergeIDsText(o RepoMergeIDsOut, verb string) string {
	var b strings.Builder
	for _, r := range o.Rows {
		b.WriteString("  " + r.Store + " " + r.Table + " " + r.ID + "  " + r.From + " → " + o.RepoID + "\n")
	}
	b.WriteString(work.Success + " " + verb + " _o.repo of " + strconv.Itoa(len(o.Rows)) + " rows to " + o.RepoID)
	if o.DryRun {
		b.WriteString("; nothing written\n")
		return b.String()
	}
	b.WriteString("; aliases " + strings.Join(o.Aliases, ", ") + " cleared from .lore/data/_meta.json (commit it with your next push)\n")
	return b.String()
}
