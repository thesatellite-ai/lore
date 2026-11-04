package wire

// Data steps of `lore migrate run` (FORMAT_EVOLUTION section 3, the 0.1.18
// step list): they change values across a table, so they run only inside a
// run, after the settings conversions and before the row rewrites.
//
//	tags_snake_case     (step 7, R13 MQ10): per store, group tags by
//	                    opstag.SnakeOf(name); keeper = smallest id byte-wise;
//	                    rename it, move the others' tag_ids bindings to it, delete them
//	db_settings_to_rows (step 12, CFG12): v0.1.17 project_configs rows of the repo
//	                    → a team policies row when the key maps; else reported, kept

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opstag "github.com/thesatellite-ai/lore/ops/tag"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Step names as the run reports them.
const (
	StepTagsSnakeCase    = "tags_snake_case"
	StepDBSettingsToRows = "db_settings_to_rows"
)

// tagFieldName is tags.name (schema/builtin.go).
const tagFieldName = "name"

// migrateTagsSnakeCase is step 7 over every attached store. It returns the
// step lines and the rows written (renamed tags, rebound rows, deleted
// duplicates). Deterministic: the keeper is the byte-wise smallest id, so
// two offline runs choose the same tag.
func (a *App) migrateTagsSnakeCase(ctx context.Context, dryRun bool) ([]string, int, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	var steps []string
	n := 0
	for i, name := range st.eng.Stores() {
		s, c, err := a.snakeTagsIn(ctx, st, name, st.backends[i], dryRun)
		if err != nil {
			return nil, 0, errs.Wrap(errs.CodeOf(err), "migrate: "+StepTagsSnakeCase+" in store "+string(name), err)
		}
		steps, n = append(steps, s...), n+c
	}
	return steps, n, nil
}

// snakeTagsIn runs step 7 in one store.
func (a *App) snakeTagsIn(ctx context.Context, st *state, src store.Source, b store.Backend, dryRun bool) ([]string, int, error) {
	tags, err := st.eng.List(ctx, src, schema.TableTags)
	if err != nil {
		return nil, 0, err
	}
	groups := map[string][]row.Row{}
	for _, t := range tags {
		if t.Deleted {
			continue
		}
		nm, ok := t.Fields[tagFieldName].(value.Text)
		if !ok {
			continue
		}
		// R13: the group key is the project too, names are unique per project.
		key := projectOf(t) + "\x00" + opstag.SnakeOf(nm.String())
		groups[key] = append(groups[key], t)
	}
	var steps []string
	n := 0
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		g := groups[key]
		slices.SortFunc(g, func(x, y row.Row) int { return strings.Compare(x.ID.String(), y.ID.String()) })
		_, snake, _ := strings.Cut(key, "\x00")
		keeper := g[0]
		cur, _ := keeper.Fields[tagFieldName].(value.Text) // grouped only when Text
		if len(g) == 1 && cur.String() == snake {
			continue
		}
		label := "store " + string(src) + ": "
		if src == store.SourceRepo {
			label = ""
		}
		// Duplicates first: the keeper takes the snake name only once no
		// other tag of the project holds it (tags are unique per project).
		for _, dup := range g[1:] {
			old, _ := dup.Fields[tagFieldName].(value.Text)
			c, err := a.rebindTag(ctx, st, src, b, dup.ID, keeper.ID, dryRun)
			if err != nil {
				return nil, 0, err
			}
			steps = append(steps, StepTagsSnakeCase+": "+label+"tag "+dup.ID.String()+" "+old.String()+" merged into "+keeper.ID.String()+" ("+strconv.Itoa(c)+" rows rebound)")
			n += c + 1
			if !dryRun {
				if _, err := st.eng.Delete(ctx, engine.Ref{Store: src, Table: schema.TableTags, ID: dup.ID}); err != nil {
					return nil, 0, err
				}
			}
		}
		if cur.String() != snake {
			steps = append(steps, StepTagsSnakeCase+": "+label+"tag "+keeper.ID.String()+" "+cur.String()+" → "+snake)
			n++
			if !dryRun {
				t, err := value.TextOf(snake)
				if err != nil {
					return nil, 0, err
				}
				if _, err := st.eng.Update(ctx, engine.Ref{Store: src, Table: schema.TableTags, ID: keeper.ID}, map[string]value.Value{tagFieldName: t}); err != nil {
					return nil, 0, err
				}
			}
		}
	}
	return steps, n, nil
}

// rebindTag moves every tag_ids binding of from to to in store src and
// returns how many rows it changes (would change under dryRun).
func (a *App) rebindTag(ctx context.Context, st *state, src store.Source, b store.Backend, from, to ids.AnyID, dryRun bool) (int, error) {
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sr := range rows {
		if sr.Deleted {
			continue
		}
		r, err := sr.Decode()
		if err != nil {
			continue // a row this lore cannot read is not rewritten (E_OLD_LORE on write)
		}
		have := value.ItemStrings(r.Fields[schema.FieldTagIDs])
		if !slices.Contains(have, from.String()) {
			continue
		}
		n++
		if dryRun {
			continue
		}
		next := make([]value.Value, 0, len(have))
		seen := map[string]bool{}
		for _, h := range have {
			if h == from.String() {
				h = to.String()
			}
			if seen[h] {
				continue
			}
			seen[h] = true
			id, perr := ids.ParseAny(h)
			if perr != nil {
				return 0, perr
			}
			ref, rerr := value.RefOf(id)
			if rerr != nil {
				return 0, rerr
			}
			next = append(next, ref)
		}
		list, err := value.ListOf(value.KindRef, next...)
		if err != nil {
			return 0, err
		}
		if _, err := st.eng.Update(ctx, engine.Ref{Store: src, Table: r.Table, ID: r.ID}, map[string]value.Value{schema.FieldTagIDs: list}); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// projectOf is a row's project_id text ("" without one).
func projectOf(r row.Row) string {
	if v, ok := r.Fields["project_id"]; ok {
		if s := value.ItemStrings(v); len(s) == 1 {
			return s[0]
		}
		if t, ok := v.(value.Text); ok {
			return t.String()
		}
	}
	return ""
}

// projectConfigsTable is the v0.1.17 settings table (dbent/schema/
// project_config.go): not a 0.1.18 table, read only by this step.
const projectConfigsTable = "project_configs"

// policyScopeRepo is the scope of a repo-level policies row (ops/settings).
const policyScopeRepo = "repo"

// v017RenderTargets is the one v0.1.17 project_configs key with a 0.1.18
// home: a JSON list of the files lore rendered, which maps to the tool
// profiles whose pointer file it names (context.targets, Q-R2-3).
const v017RenderTargets = "render_targets"

// v1ProjectConfig is the part of a v0.1.17 project_configs row the step reads.
type v1ProjectConfig struct {
	Key   string  `json:"key"`
	Value *string `json:"value"`
}

// migrateDBSettings is step 12 (CFG12) for the repo store: each v0.1.17
// project_configs row whose key maps to a team rule becomes one policies
// row (written once: a repo that already holds the key gets none); every
// other key is reported and left in place, never guessed.
func (a *App) migrateDBSettings(ctx context.Context, dryRun bool) ([]string, int, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, _, err := st.backends[0].Read(ctx, "")
	if err != nil {
		return nil, 0, err
	}
	var steps []string
	n := 0
	for _, sr := range rows {
		if sr.Table.String() != projectConfigsTable || sr.Deleted {
			continue
		}
		var c v1ProjectConfig
		if json.Unmarshal(sr.Doc, &c) != nil || c.Key == "" {
			steps = append(steps, StepDBSettingsToRows+": "+projectConfigsTable+"/"+sr.ID.String()+" is not a v0.1.17 setting; left in place")
			continue
		}
		targets, ok := renderTargetsOf(c)
		if !ok {
			steps = append(steps, StepDBSettingsToRows+": "+projectConfigsTable+" key "+c.Key+" has no 0.1.18 home; left in place")
			continue
		}
		have, err := a.hasRepoPolicy(ctx, st, policy.KeyContextTargets)
		if err != nil {
			return nil, 0, err
		}
		line := StepDBSettingsToRows + ": " + projectConfigsTable + " " + c.Key + " → policy " + string(policy.KeyContextTargets) + " = " + strings.Join(targets, ",")
		if have {
			steps = append(steps, line+" (the repo already sets it; kept)")
			continue
		}
		steps = append(steps, line)
		n++
		if dryRun {
			continue
		}
		fields, err := textListPolicy(policy.KeyContextTargets, targets, policyScopeRepo)
		if err != nil {
			return nil, 0, err
		}
		if _, err := st.eng.Create(ctx, store.SourceRepo, schema.TablePolicies, fields); err != nil {
			return nil, 0, errs.Wrap(errs.CodeOf(err), "migrate: write the "+string(policy.KeyContextTargets)+" policy", err)
		}
	}
	return steps, n, nil
}

// renderTargetsOf maps a render_targets setting to tool profile names:
// every listed file must be a built-in profile's pointer file, else the
// key is not classified (never guessed).
func renderTargetsOf(c v1ProjectConfig) ([]string, bool) {
	if c.Key != v017RenderTargets || c.Value == nil {
		return nil, false
	}
	var files []string
	if json.Unmarshal([]byte(*c.Value), &files) != nil || len(files) == 0 {
		return nil, false
	}
	var out []string
	for _, f := range files {
		i := slices.IndexFunc(toolprofile.Builtins(), func(p toolprofile.Profile) bool { return p.PointerFile == f })
		if i < 0 {
			return nil, false
		}
		name := string(toolprofile.Builtins()[i].Name)
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, true
}

// hasRepoPolicy reports whether the repo holds a live policies row of key.
func (a *App) hasRepoPolicy(ctx context.Context, st *state, key policy.Key) (bool, error) {
	rows, err := st.eng.List(ctx, store.SourceRepo, schema.TablePolicies)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(rows, func(r row.Row) bool {
		k, ok := r.Fields[policyFieldKey].(value.Text)
		return !r.Deleted && ok && k.String() == string(key)
	}), nil
}

// textListPolicy builds a policies row whose value is a JSON list of text.
func textListPolicy(key policy.Key, items []string, scope string) (map[string]value.Value, error) {
	f, err := clientRoutesPolicy(items, scope)
	if err != nil {
		return nil, err
	}
	k, err := value.TextOf(string(key))
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: policy key", err)
	}
	f[policyFieldKey] = k
	return f, nil
}
