package task

import (
	"slices"
	"strconv"
	"strings"
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

// taskKeys are the keys of task list --where / --sort: every visible field
// of the tasks table (internal and sensitive ones and JSON blobs are left
// out: they are not something a person filters on) plus the envelope keys.
// Built from schema.TableTasks, so a new task field is filterable at once.
func taskKeys() map[string]filter.Field {
	out := map[string]filter.Field{
		keyID:        {Kind: value.KindText},
		keyCreatedAt: {Kind: value.KindDateTime},
		keyUpdatedAt: {Kind: value.KindDateTime},
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
// name is a task field). Metadata keys (meta.<key>, or a bare name no
// field carries) are refused with E_NOT_IMPLEMENTED: the new core has no
// metadata reader for task rows yet, and an empty result would read as
// "no task matches" (skeleton honesty).
func listMatcher(where []string, keys map[string]filter.Field) (filter.Matcher, error) {
	f, err := filter.Parse(where)
	if err != nil {
		return filter.Matcher{}, err
	}
	return f.Bind(func(k filter.Key) (filter.Field, error) {
		if fd, ok := keys[k.Name]; ok && !k.Meta {
			return fd, nil
		}
		if k.Meta {
			return filter.Field{}, errs.New(errs.NotImplemented, "task list --where "+k.String()+": metadata keys are not filterable on task list yet").
				WithHint("filter on a task field: " + strings.Join(sortedKeys(keys), ", "))
		}
		return filter.Field{}, filter.UnknownKey(k, sortedKeys(keys))
	})
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
