package task

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/logic/filter"
	"github.com/thesatellite-ai/lore/logic/sortspec"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// Envelope keys task list --where and --sort accept beside the table's
// own fields (row.Row's audit times and id).
const (
	keyID        = "id"
	keyCreatedAt = "created_at"
	keyUpdatedAt = "updated_at"
)

// People keys of --where (16.1): assignee (the lead counts, 14.2: the
// lead is always an assignee), lead, watcher; aliases of the row fields.
const (
	keyAssignee = "assignee"
	keyLead     = "lead"
	keyWatcher  = "watcher"
)

// taskKeys are the keys of task list --where / --sort: every visible field
// of the tasks table (internal and sensitive ones and JSON blobs are left
// out: they are not something a person filters on) plus the envelope keys.
// Built from schema.TableTasks, so a new task field is filterable at once.
func taskKeys() map[string]filter.Field {
	out := map[string]filter.Field{
		keyID:        {Kind: value.KindText},
		keyCreatedAt: {Kind: value.KindDateTime},
		keyUpdatedAt: {Kind: value.KindDateTime},
		// 18.4: the SLA state, computed on read (views.go withSLA).
		keySLA: {Kind: value.KindText, Values: SLAStates()},
		// 16.1 people keys (rowGetter reads them).
		keyAssignee: {Kind: value.KindList, Elem: value.KindText},
		keyLead:     {Kind: value.KindText},
		keyWatcher:  {Kind: value.KindList, Elem: value.KindText},
	}
	for _, t := range schema.Builtin() {
		if t.Name != table {
			continue
		}
		for _, f := range t.Fields {
			if f.Internal || f.Sensitive || f.Kind == value.KindJSON {
				continue
			}
			out[f.Name] = filter.Field{Kind: f.Kind, Elem: f.Elem, Values: f.Values}
		}
		// 9.1: the estimate is stored as its typed decimal text ("3.50"),
		// but it is a number: --sort estimate and estimate>3 compare it
		// numerically, never as text ("10" after "9").
		out[fieldEstimate] = filter.Field{Kind: value.KindNumber}
	}
	return out
}

// sortedKeys lists keys by name, for error messages.
func sortedKeys(m map[string]filter.Field) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// listMatcher binds task list --where (XQ7: the one filter grammar; a bare
// name is a task field). meta.<key> terms are returned apart, in the
// --meta grammar, for the metadata filter (METADATA 3.7) that list and
// bulk edit run after the field match; a bare name no field carries is
// an unknown key (P2-TASK-1: never silently a metadata key).
func listMatcher(where []string, keys map[string]filter.Field) (filter.Matcher, []string, error) {
	f, err := filter.Parse(where)
	if err != nil {
		return filter.Matcher{}, nil, err
	}
	return bindWhere(f, keys)
}

// whereMatcher is listMatcher at an op that knows the caller: a value
// @me becomes the caller's actor id first (16.1 `assignee:@me`, 16.2 a
// view's @me resolves for whoever runs it); an unknown caller is the same
// refusal --assignee @me gives.
func (o ops) whereMatcher(ctx context.Context, where []string, keys map[string]filter.Field) (filter.Matcher, []string, error) {
	f, err := filter.Parse(where)
	if err != nil {
		return filter.Matcher{}, nil, err
	}
	if f.HasValue(filter.Me) {
		me, merr := o.actorRef(ctx, actorMe)
		if merr != nil {
			return filter.Matcher{}, nil, merr
		}
		f = f.Replace(filter.Me, me)
	}
	return bindWhere(f, keys)
}

// bindWhere binds a parsed task --where to the task keys; meta.<key>
// terms come back as --meta filter arguments (r3-grammar: the metadata
// reader on task rows is metalist, the same one --meta uses).
func bindWhere(f filter.Filter, keys map[string]filter.Field) (filter.Matcher, []string, error) {
	fields, metaArgs, err := splitMeta(f)
	if err != nil {
		return filter.Matcher{}, nil, err
	}
	m, err := fields.Bind(func(k filter.Key) (filter.Field, error) {
		if fd, ok := keys[k.Name]; ok {
			return fd, nil
		}
		return filter.Field{}, filter.UnknownKey(k, sortedKeys(keys))
	})
	return m, metaArgs, err
}

// splitMeta moves the meta.<key> terms of f into --meta grammar arguments
// (`key<op>value`; the operator spellings are the same in both grammars).
// A term with several OR-ed values has no --meta form yet (`<<` is not
// supported by the metadata filter), so it is refused rather than read as
// one value.
func splitMeta(f filter.Filter) (filter.Filter, []string, error) {
	var rest filter.Filter
	var out []string
	for _, t := range f.Terms {
		if !t.Key.Meta {
			rest.Terms = append(rest.Terms, t)
			continue
		}
		if len(t.Values) > 1 {
			return filter.Filter{}, nil, errs.New(errs.InvalidInput, "task list --where "+t.Raw+": a metadata key takes one value here").
				WithHint("repeat the list with one value each, or use lore sql over row_meta")
		}
		arg := t.Key.Name + string(t.Op)
		if len(t.Values) == 1 {
			arg += t.Values[0]
		}
		out = append(out, arg)
	}
	return rest, out, nil
}

// rowGetter reads a task row's values for the matcher.
func rowGetter(keys map[string]filter.Field) func(r row.Row) filter.Getter {
	return func(r row.Row) filter.Getter {
		return func(k filter.Key) []string {
			switch k.Name {
			case keyID:
				return []string{r.ID.String()}
			case keyCreatedAt:
				return timeText(r.CreatedAt)
			case keyUpdatedAt:
				return timeText(r.UpdatedAt)
			case keyAssignee:
				p := peopleOf(r)
				return p.assignees
			case keyLead:
				if l := work.Field(r, fieldAssigned); l != "" {
					return []string{l}
				}
				return nil
			case keyWatcher:
				return value.ItemStrings(r.Fields[schema.FieldTaskWatchers])
			}
			if keys[k.Name].Kind == value.KindList {
				return value.ItemStrings(r.Fields[k.Name])
			}
			if s := work.Field(r, k.Name); s != "" {
				return []string{s}
			}
			return nil
		}
	}
}

// timeText is a set time as one RFC 3339 value, none when zero.
func timeText(t time.Time) []string {
	if t.IsZero() {
		return nil
	}
	return []string{t.UTC().Format(time.RFC3339Nano)}
}

// listSort parses and checks task list --sort (XQ10).
func listSort(raw []string, keys map[string]filter.Field) ([]sortspec.Spec, error) {
	specs, err := sortspec.Parse(raw)
	if err != nil {
		return nil, err
	}
	return specs, sortspec.Check(specs, sortedKeys(keys))
}

// sortValue types a task's value for --sort: an enum by its declared order
// (priority:desc puts urgent first), numbers numerically, times by
// instant, the rest as text.
func sortValue(keys map[string]filter.Field, r row.Row) func(string) sortspec.Value {
	return func(key string) sortspec.Value {
		switch key {
		case keyID:
			return sortspec.Text(r.ID.String())
		case keyCreatedAt:
			return sortspec.Time(r.CreatedAt)
		case keyUpdatedAt:
			return sortspec.Time(r.UpdatedAt)
		}
		fd := keys[key]
		s := work.Field(r, key)
		if s == "" {
			return sortspec.Missing
		}
		if len(fd.Values) > 0 {
			if i := slices.Index(fd.Values, s); i >= 0 {
				return sortspec.Rank(i)
			}
		}
		switch fd.Kind {
		case value.KindNumber:
			if n, err := strconv.ParseFloat(s, 64); err == nil {
				return sortspec.Number(n)
			}
		case value.KindDate, value.KindDateTime:
			return sortspec.Time(work.Time(r, key))
		default:
			// text, refs and lists sort as their text form.
		}
		return sortspec.Text(s)
	}
}
