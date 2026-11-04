package knowledge

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/ops/internal/tagref"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// scope is which rows a read returns by their repo_id (v0.1.17
// saas/cmd/cli/repo_scope.go, as ops/memory keeps it).
type scope string

// Scopes.
const (
	scopeMaster   scope = "master"
	scopeInherit  scope = "inherit"
	scopeRepoOnly scope = "repo_only"
	scopeAll      scope = "all"
)

// scopeOf applies v0.1.17's precedence: --all-repos, then --master-only,
// then --no-inherit (only with a repo), then --repo; else project-wide.
func scopeOf(repoID string, noInherit, masterOnly, allRepos bool) scope {
	switch {
	case allRepos:
		return scopeAll
	case masterOnly:
		return scopeMaster
	case noInherit && repoID != "":
		return scopeRepoOnly
	case repoID != "":
		return scopeInherit
	}
	return scopeMaster
}

// keeps reports whether a row with repo_id got belongs in the scope of
// repo want.
func (s scope) keeps(got, want string) bool {
	switch s {
	case scopeAll:
		return true
	case scopeInherit:
		return got == "" || got == want
	case scopeRepoOnly:
		return got == want
	case scopeMaster:
	}
	return got == ""
}

// Field returns the text form of a row field ("" when absent or null).
func Field(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// Number is a number field, 0 when absent.
func Number(r row.Row, name string) float64 {
	n, ok := r.Fields[name].(value.Number)
	if !ok {
		return 0
	}
	f, err := n.Float64()
	if err != nil {
		return 0
	}
	return f
}

// Time is a datetime field (zero when absent).
func Time(r row.Row, name string) time.Time {
	switch v := r.Fields[name].(type) {
	case value.DateTime:
		return v.Time()
	case value.Text:
		if dt, err := value.DateTimeOf(v.String()); err == nil {
			return dt.Time()
		}
	}
	return time.Time{}
}

// Archived reports whether the row has archived_at set.
func Archived(r row.Row) bool { return Field(r, FieldArchivedAt) != "" }

// CreatedBy is created_by_actor_id, else the envelope's created_by (the
// same actor for a 0.1.18 row that did not set the column).
func CreatedBy(r row.Row) string {
	if s := Field(r, FieldCreatedBy); s != "" {
		return s
	}
	if !r.CreatedBy.IsZero() {
		return r.CreatedBy.String()
	}
	return ""
}

// Body returns the text from --body or the positional argument; exactly
// one must be given and it may not be blank (E_EMPTY_BODY).
func Body(n Noun, flag, arg string) (string, error) {
	if flag != "" && arg != "" {
		return "", errs.New(errs.InvalidInput, n.Name+" add: give the body once, as an argument or with --body, not both")
	}
	body := flag + arg
	if strings.TrimSpace(body) == "" {
		return "", errs.New(errs.EmptyBody, n.Name+" add: the body is empty").
			WithHint("lore " + n.Name + ` add --body="<text>"`)
	}
	return body, nil
}

// Required refuses a blank required flag (v0.1.17: `required flag(s) "x"
// not set`).
func Required(n Noun, flag, v string) error {
	if strings.TrimSpace(v) == "" {
		return errs.New(errs.InvalidInput, n.Name+": --"+flag+" is required")
	}
	return nil
}

// OneOf refuses a value outside allowed ("" passes: not given).
func OneOf(n Noun, flag, v string, allowed []string) error {
	if v == "" || slices.Contains(allowed, v) {
		return nil
	}
	return errs.New(errs.InvalidInput, n.Name+": --"+flag+" "+Quote(v)+" is not one of "+strings.Join(allowed, " | "))
}

// Texts turns the non-empty strings of m into text values (an empty string
// means "not given"), in a stable order.
func Texts(n Noun, m map[string]string) (map[string]value.Value, error) {
	out := map[string]value.Value{}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if m[k] == "" {
			continue
		}
		t, err := value.TextOf(m[k])
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), n.Name+": field "+k, err)
		}
		out[k] = t
	}
	return out, nil
}

// Quote renders a user-given value for an error, truncated.
func Quote(s string) string {
	return `"` + textcut.Cut(s, maxQuoted, maxQuoted) + `"`
}

// Cut is v0.1.17's trunc: s cut to n bytes, ending in "...", never inside
// a UTF-8 character (textcut.Cut; ASCII output is v0.1.17's bytes).
func Cut(s string, n int) string {
	return textcut.Cut(s, n, n-len(ellipsis))
}

// SourceKey is the v0.1.17 `_source` key of a row from a store other
// than the repo's own ("" for the repo).
func SourceKey(src store.Source) string {
	if src == store.SourceRepo {
		return ""
	}
	return string(src)
}

// Warning is one engine warning copied into an output (W_SCAN_DEGRADED).
type Warning struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Warnings copies a write's warnings.
func Warnings(w engine.Written) []Warning {
	var out []Warning
	for _, x := range w.Warnings {
		out = append(out, Warning{Code: string(x.Code), Field: x.Field, Message: x.Message})
	}
	return out
}

// TagRefs resolves tag names of project to their ids; each must exist (a
// typo never mints a tag).
func TagRefs(ctx context.Context, env Env, n Noun, project string, names []string) ([]ids.AnyID, error) {
	out := make([]ids.AnyID, 0, len(names))
	for _, nm := range names {
		nm = strings.TrimSpace(nm)
		if nm == "" {
			return nil, errs.New(errs.InvalidInput, n.Name+": an empty --tag")
		}
		found, err := tagref.Find(ctx, env, project, nm)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, errs.New(errs.NotFound, n.Name+": tag "+Quote(nm)+" not found in this project").
				WithHint("create it with `lore tag add --name=" + nm + "`")
		}
		id := found[0]
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// TagList builds the tag_ids value (Null when empty: the field is cleared).
func TagList(list []ids.AnyID) (value.Value, error) {
	if len(list) == 0 {
		return value.Null{}, nil
	}
	items := make([]value.Value, 0, len(list))
	for _, id := range list {
		r, err := value.RefOf(id)
		if err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return value.ListOf(value.KindRef, items...)
}

// TagsOf is a row's tag ids.
func TagsOf(r row.Row) []string {
	l, ok := r.Fields[schema.FieldTagIDs].(value.List)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(l.Items()))
	for _, it := range l.Items() {
		out = append(out, it.String())
	}
	return out
}

// HasTags is a List keep func: the row carries every named tag (any tag
// of that name in the row's project, in any attached store; nil when no
// names are given).
func HasTags(ctx context.Context, env Env, names []string) (func(row.Row) bool, error) {
	if len(names) == 0 {
		return nil, nil
	}
	idx, err := tagref.Load(ctx, env)
	if err != nil {
		return nil, err
	}
	return func(r row.Row) bool {
		have := TagsOf(r)
		for _, nm := range names {
			if !idx.Has(have, Field(r, FieldProject), strings.TrimSpace(nm)) {
				return false
			}
		}
		return true
	}, nil
}

// retagPatch is r's new tag_ids: its tags plus add, minus drop.
func retagPatch(ctx context.Context, env Env, n Noun, r row.Row, add, drop []string) (value.Value, error) {
	project := Field(r, FieldProject)
	addIDs, err := TagRefs(ctx, env, n, project, add)
	if err != nil {
		return nil, err
	}
	dropIDs := make([]ids.AnyID, 0, len(drop))
	for _, nm := range drop {
		t, terr := tagref.Droppable(ctx, env, project, strings.TrimSpace(nm))
		if terr != nil {
			return nil, terr
		}
		dropIDs = append(dropIDs, t...)
	}
	var out []ids.AnyID
	for _, s := range TagsOf(r) {
		id, perr := ids.ParseAny(s)
		if perr != nil || slices.Contains(dropIDs, id) {
			continue
		}
		out = append(out, id)
	}
	for _, id := range addIDs {
		if !slices.Contains(out, id) && !slices.Contains(dropIDs, id) {
			out = append(out, id)
		}
	}
	return TagList(out)
}

// Or returns s, or def when s is empty.
func Or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ScopeLabel is v0.1.17's scope badge text: repo:<mount> or master.
func ScopeLabel(repo string) string {
	if repo == "" {
		return "master"
	}
	return "repo:" + repo
}

// StoreLabel is the " [<store>]" suffix a human line shows for a row
// outside the repo store ("" for the repo).
func StoreLabel(src string) string {
	if src == "" || src == string(store.SourceRepo) {
		return ""
	}
	return " [" + src + "]"
}

// AddLabel is the store label of v0.1.17's add line ("✓ <id> <label>"):
// "[<store>]" for a row outside the repo store, "" for the repo, so the
// caller's format keeps v0.1.17's separating space either way.
func AddLabel(src string) string {
	return strings.TrimPrefix(StoreLabel(src), " ")
}
