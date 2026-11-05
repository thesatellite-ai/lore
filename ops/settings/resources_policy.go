package settings

import (
	"cmp"
	"context"
	"encoding/json"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store"
)

// Resource write policy (RESOURCES_DESIGN 3.7, 3.15):
//
//	resAdd / resClone / resImport
//	  └─ checkPlace(target)   resources.allowed_in ∋ class(target)? else E_RESOURCE_PLACE (RQ2)
//	  └─ engine write         resources.max_bytes refuses (engine/policy.go)
//	  └─ sizeWarning(fields)  resources.warn_bytes → one warning line (RQ4)

// StoreClasser is an optional Env upgrade: the F15 class of an attached
// store (wire.App.StoreClass). Without it a store is classed by name only
// (repo → repo, anything else → store), which can never widen allowed_in
// past what the name says.
type StoreClasser interface {
	StoreClass(ctx context.Context, name store.Source) (direction.Class, error)
}

// checkedBy values of resources.checked_by (RQ8): who stamped last_checked.
const (
	CheckedManual = "manual"
	CheckedEdit   = "edit"
	// rCheckedBy is the resources column (schema.Builtin, 8ceb3ee).
	rCheckedBy = "checked_by"
)

// placeOf is target's resources.allowed_in item: a client-class store is
// "client", the repo store of our own repo "repo", any other store "store".
func placeOf(ctx context.Context, env Env, target store.Source) (string, error) {
	if sc, ok := env.(StoreClasser); ok {
		c, err := sc.StoreClass(ctx, target)
		if err != nil {
			return "", err
		}
		if c == direction.Client {
			return policy.PlaceClient, nil
		}
	}
	if target == store.SourceRepo {
		return policy.PlaceRepo, nil
	}
	return policy.PlaceStore, nil
}

// checkPlace refuses a resource write into a place resources.allowed_in
// leaves out (RQ2, OQ24: the levels intersect). No Policier: the default
// (every place) applies.
func checkPlace(ctx context.Context, env Env, target store.Source) error {
	p, ok := env.(bulk.Policier)
	if !ok {
		return nil
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return err
	}
	allowed := set.Strings(policy.KeyResourcesAllowedIn)
	place, err := placeOf(ctx, env, target)
	if err != nil {
		return err
	}
	if slices.Contains(allowed, place) {
		return nil
	}
	where := ""
	if eff, found := set.Get(policy.KeyResourcesAllowedIn); found && !eff.IsDefault {
		where = "; set at level " + string(eff.Level) + " (" + eff.Source.String() + ")"
	}
	return errs.New(errs.ResourcePlace, "resources are not allowed in "+place+" places ("+string(target)+"; resources.allowed_in: "+
		strings.Join(allowed, ", ")+where+")").WithHint("write in an allowed place (--to); lore policy show resources")
}

// sizeWarning is the RQ4 warning line for a row whose values pass
// resources.warn_bytes ("" when under it or without a Policier). Measured
// like the engine's max_bytes check: the canonical {name: value} object of
// the row's fields.
func sizeWarning(ctx context.Context, env Env, target store.Source, fields map[string]value.Value) string {
	p, ok := env.(bulk.Policier)
	if !ok {
		return ""
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return ""
	}
	limit, ok := set.Limit(policy.KeyResourcesWarnBytes)
	if !ok {
		return ""
	}
	ms := make([]canon.Member, 0, len(fields))
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		raw, merr := fields[k].MarshalJSON()
		if merr != nil {
			return ""
		}
		ms = append(ms, canon.Member{Key: k, Raw: raw})
	}
	obj, err := canon.Object(ms)
	if err != nil || int64(len(obj)) <= limit {
		return ""
	}
	return "warning: this resource is " + strconv.Itoa(len(obj)) + " bytes, over resources.warn_bytes " +
		strconv.FormatInt(limit, 10) + "; keep resources small (long text belongs in a doc or memory)\n"
}

// maxResStdin caps a --set-stdin value (the resource size limit's
// default: a larger value would be refused at write anyway).
const maxResStdin = 16 * 1024

// resStdin is where --set-stdin reads (a var so tests can swap it).
var resStdin io.Reader = os.Stdin

// valueSets folds --set-json and --set-stdin into --set's field=value
// pairs (RES-OQ4: sensitive values may also go on the command line; these
// two keep them out of shell history). A --set-json value that is a JSON
// string becomes its text; any other JSON is passed as JSON text, which
// list, json, number and bool fields parse.
func valueSets(sets, setJSON []string, setStdin string) ([]string, error) {
	out := slices.Clone(sets)
	for _, s := range setJSON {
		f, raw, ok := strings.Cut(s, setSep)
		if !ok || f == "" || raw == "" {
			return nil, errs.New(errs.InvalidInput, "--set-json "+quote(s)+": want field=<json>")
		}
		if !json.Valid([]byte(raw)) {
			return nil, errs.New(errs.InvalidInput, "--set-json "+f+": the value is not JSON")
		}
		var str string
		if json.Unmarshal([]byte(raw), &str) == nil {
			raw = str
		}
		out = append(out, f+setSep+raw)
	}
	if setStdin != "" {
		b, err := io.ReadAll(io.LimitReader(resStdin, maxResStdin+1))
		if err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "--set-stdin "+setStdin+": read stdin", err)
		}
		if len(b) > maxResStdin {
			return nil, errs.New(errs.InvalidInput, "--set-stdin "+setStdin+": more than "+strconv.Itoa(maxResStdin)+" bytes")
		}
		v := strings.TrimRight(string(b), "\r\n")
		if v == "" {
			return nil, errs.New(errs.InvalidInput, "--set-stdin "+setStdin+": stdin was empty")
		}
		out = append(out, setStdin+setSep+v)
	}
	return out, nil
}

// staleAfterHours is resources.stale_after's default (RES 3.6: 24 h),
// used only when the Env has no policy reader; shortCommitLen is the
// commit prefix the note prints.
const (
	staleAfterHours = 24
	shortCommitLen  = 12
)

// freshener is the optional Env upgrade wire implements (App.StoreFresh):
// an attached store's fetch stamp (store.Freshness).
type freshener interface {
	StoreFresh(ctx context.Context, source store.Source) (store.Fresh, error)
}

// staleNote is the RES 3.6 line of a resource write checked against
// store kind rows fetched longer ago than resources.stale_after (policy
// of the write's target): "checked against store kinds as of <commit>".
// The write itself is never refused (resources can be written offline).
// P2-RES-8: only the store holding the kind row is asked; a store with no
// stamp (a folder store, the repo store, never fetched) adds nothing.
func (o ops) staleNote(ctx context.Context, d kindDef, target store.Source) string {
	f, ok := upgrade.As[freshener](o.env)
	if !ok {
		return ""
	}
	fr, err := f.StoreFresh(ctx, d.kind.Src)
	if err != nil || !fr.Known {
		return ""
	}
	hours := int64(staleAfterHours)
	if p, isP := o.env.(bulk.Policier); isP {
		if set, perr := p.Policy(ctx, target); perr == nil {
			if n, set := set.Limit(policy.KeyResourcesStaleAfter); set {
				hours = n
			}
		}
	}
	age := opclock.Now(o.env).Sub(fr.FetchedAt)
	if age <= time.Duration(hours)*time.Hour {
		return ""
	}
	commit := fr.Commit
	if len(commit) > shortCommitLen {
		commit = commit[:shortCommitLen]
	}
	return "  note: checked against store kinds as of " + cmp.Or(commit, "an unknown commit") + " (" + string(d.kind.Src) +
		", last fetched " + fr.FetchedAt.UTC().Format(time.DateOnly) + "; run lore sync)\n"
}
