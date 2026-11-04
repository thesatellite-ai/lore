package engine

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The POLICY step of spec 9.2 (validate → resolve target → permission →
// scan → POLICY → confirm → stamp → write). Every key of logic/policy.Keys
// that decides something at write time is enforced here, on the one path
// the CLI and REST share, so neither surface can skip it:
//
//   - agents.person_only: an operation any level lists is refused for an
//     agent caller with E_POLICY_DENIED (AGENTS POLICY: "the only way an
//     agent is ever refused"); checked in Run, because it names commands,
//     not rows (personOnly).
//   - secrets.on_detect and the D2/D9 secret rule levels incl. BATCH 8: in
//     the scan (screen), which this step follows.
//   - resources.max_bytes: a resources row whose values are larger than the
//     limit is refused (RQ4, RESOURCES_DESIGN 3.15: "refused at write");
//     enforceRow.
//
// Keys that need no write-time step are listed in spec-deltas.md with the
// reason (meta.max_bytes: the write API takes no metadata yet; tasks.max_depth:
// tasks have no parent field; routing.client_repos: applied by the target
// resolution of wire, before the engine sees the write; the rest decide at
// read, reveal, scanner build or trash time).

// personOnlySep joins an operation name and one of its input fields in an
// agents.person_only item that names a flag ("meta.edit.merge_into",
// AGENT_IDENTITY_DESIGN 8.2: "plus a flag where one flag changes the
// meaning").
const personOnlySep = "."

// personOnly refuses an agent caller of an operation agents.person_only
// lists, at any level (the key is a set: every level's items join, so the
// stricter answer always wins). A person is never refused by it. The
// message names the rule, the row that set it, the level and what lore saw;
// never a field value. DOCS O9: this list is the one way to keep agents from
// a command (it replaced meta.agents_new_keys).
func personOnly(pol policy.Set, spec op.Spec, in op.Input, c op.Caller) error {
	if c.Agent == "" {
		return nil
	}
	listed := pol.Strings(policy.KeyAgentsPersonOnly)
	if len(listed) == 0 {
		return nil
	}
	hit := ""
	if slices.Contains(listed, spec.Name) {
		hit = spec.Name
	} else {
		for _, item := range listed {
			flag, ok := strings.CutPrefix(item, spec.Name+personOnlySep)
			if ok && flag != "" && inputSets(in, flag) {
				hit = item
				break
			}
		}
	}
	if hit == "" {
		return nil
	}
	where := ""
	if eff, ok := pol.Get(policy.KeyAgentsPersonOnly); ok && !eff.Source.IsZero() {
		where = " (policy " + eff.Source.String() + ", level " + string(eff.Level) + ")"
	}
	return errs.New(errs.PolicyDenied, "engine: "+string(policy.KeyAgentsPersonOnly)+" lists "+hit+where+"; this looks like "+strconv.Quote(c.Agent)).
		WithHint("a person runs it in their own terminal")
}

// inputSets reports whether the input field named flag is set: a bool
// field that is true, or a text field that is not empty.
func inputSets(in op.Input, flag string) bool {
	if v, ok := in.Bool(flag); ok {
		return v
	}
	for _, tv := range in.Texts() {
		if tv.Field == flag && tv.Text != "" {
			return true
		}
	}
	return false
}

// enforceRow applies the row-level write-time policies to the fields about
// to be stored (after the scan, so a redacted value is what is measured).
func enforceRow(pol policy.Set, def schema.TableDef, fields map[string]value.Value) error {
	if def.Name != schema.TableResources {
		return nil
	}
	limit, ok := pol.Limit(policy.KeyResourcesMaxBytes)
	if !ok {
		return nil
	}
	n, err := valuesSize(fields)
	if err != nil {
		return err
	}
	if int64(n) > limit {
		return errs.New(errs.ResourceInvalid, "engine: the resource would be "+strconv.Itoa(n)+" bytes; the limit is "+
			strconv.FormatInt(limit, 10)+" ("+string(policy.KeyResourcesMaxBytes)+")").
			WithHint("keep a resource small: put long text in a doc or memory and link it")
	}
	return nil
}

// valuesSize is the size of a row's values as the canonical JSON object
// {name: value} of every field (D22: names and values only, no envelope),
// so the measure does not depend on ids, HLCs or field order.
func valuesSize(fields map[string]value.Value) (int, error) {
	ms := make([]canon.Member, 0, len(fields))
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		raw, err := fields[k].MarshalJSON()
		if err != nil {
			return 0, errs.Wrap(errs.Internal, "engine: measure field "+k, err)
		}
		ms = append(ms, canon.Member{Key: k, Raw: raw})
	}
	obj, err := canon.Object(ms)
	if err != nil {
		return 0, errs.Wrap(errs.Internal, "engine: measure the row", err)
	}
	return len(obj), nil
}

// storeMe is the personal store's name (`--to me`): resources are team
// facts, never personal ones (RESOURCES 3.7; ops/settings refuses the same).
const storeMe store.Source = "me"

// enforceMovePlace applies resources.allowed_in to a move of a resource
// (RQ2, RESOURCES 3.7/3.9): the target's place class (client repo, our own
// repo, any other store) must be one the target's effective policy lists, the
// same rule resource add/import/clone apply in ops/settings checkPlace, so
// `lore move` cannot carry a resource where it could not be written. pol is
// the TARGET's policy; client is the target's F15 class.
func enforceMovePlace(pol policy.Set, def schema.TableDef, to store.Source, client bool) error {
	if def.Name != schema.TableResources {
		return nil
	}
	if to == storeMe {
		return errs.New(errs.ResourcePlace, "engine: --to me: resources are team facts, not personal ones").WithHint("--to repo or --to <store>[/<folder>]")
	}
	place := policy.PlaceStore
	switch {
	case client:
		place = policy.PlaceClient
	case to == store.SourceRepo:
		place = policy.PlaceRepo
	}
	allowed := pol.Strings(policy.KeyResourcesAllowedIn)
	if slices.Contains(allowed, place) {
		return nil
	}
	where := ""
	if eff, ok := pol.Get(policy.KeyResourcesAllowedIn); ok && !eff.IsDefault {
		where = "; set at level " + string(eff.Level) + " (" + eff.Source.String() + ")"
	}
	return errs.New(errs.ResourcePlace, "engine: resources are not allowed in "+place+" places ("+string(to)+"; "+
		string(policy.KeyResourcesAllowedIn)+": "+strings.Join(allowed, ", ")+where+"); nothing moved").
		WithHint("move it to an allowed place; lore policy show resources")
}
