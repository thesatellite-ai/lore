package lint

// staleclaim.go — stale_claim: a live row says something is planned or
// not done yet, and a newer live row on the same subject says it is done
// (devco:D-1027, bug hunt H-289). Both are rendered and found; an agent
// reading the older one first answers "not built yet". stale_fact catches
// a fact whose code evidence drifted; this one needs no evidence keys.
//
//	staleClaims(all, now)
//	  ├─ every live, unarchived row of claimNouns' tables, one copy each
//	  ├─ logic/staleclaim: a planned clause, its rare subject words, a newer
//	  │  row with a done clause holding them (staleclaim's file comment)
//	  └─ one finding on the OLDER row naming the newer one; fix = archive
//	     the older row once the newer one is read (lore <kind> archive --id)
//
// Reported, never changed on its own: the newer row may be the wrong one.

import (
	"cmp"
	"maps"
	"slices"
	"time"

	"github.com/thesatellite-ai/lore/logic/staleclaim"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// StaleClaim is the finding of a planned claim a newer row says is done.
const StaleClaim Code = "stale_claim"

// claimNouns maps each table stale_claim scans to the command noun whose
// `archive --id` verb hides a row of it: near_duplicate's kinds and the
// other kinds lore ask reads (an architecture note saying a read is
// planned was the case that asked for this check).
var claimNouns = func() map[row.Table]string {
	m := maps.Clone(archiveNouns)
	m[schema.TableArchitectureNotes] = "architecturenote"
	m[schema.TableCookbookRecipes] = "cookbookrecipe"
	m[schema.TableIncidents] = "incident"
	m[schema.TableWorkflows] = "workflow"
	return m
}()

// ClaimNouns lists the command nouns stale_claim prints in its fix
// (`lore <noun> archive --id`), sorted; a test checks each verb exists.
func ClaimNouns() []string {
	return slices.Sorted(maps.Values(claimNouns))
}

// staleClaims reports every live row whose planned / not-yet claim a
// newer live row on the same subject states as done, oldest row first;
// a row past its valid_until at now is history and left out.
func staleClaims(all map[row.Table][]sourced, now time.Time) []Finding {
	var rows []sourced
	for t := range claimNouns {
		for _, s := range all[t] {
			if text(s.r, fArchivedAt) == "" {
				rows = append(rows, s)
			}
		}
	}
	rows = oneCopyEach(rows) // devco:D-929: two copies of one row are copied_id
	slices.SortFunc(rows, func(a, b sourced) int {
		return cmp.Or(a.r.CreatedAt.Compare(b.r.CreatedAt), cmp.Compare(a.r.ID.String(), b.r.ID.String()))
	})
	docs := make([]staleclaim.Doc, 0, len(rows))
	at := make(map[string]sourced, len(rows))
	for _, s := range rows {
		if schema.RowHistory(s.r, now) != "" {
			continue // superseded or past valid_until: already history
		}
		id := s.r.ID.String()
		at[id] = s
		docs = append(docs, staleclaim.Doc{ID: id, Title: text(s.r, fTitle), Body: text(s.r, fBody), At: schema.RowDate(s.r), State: staleclaim.StateOf(s.r)})
	}
	var out []Finding
	for _, f := range staleclaim.NewIndex(docs, nil).FindAll() {
		o, n := at[f.ID], at[f.By]
		noun := claimNouns[o.r.Table]
		out = append(out, Finding{Code: StaleClaim, ID: f.ID, Store: string(o.src),
			Message: f.ID + " says it is planned or not done yet, and the newer " + f.By + " (" + string(n.src) + ", " + f.ByAt.UTC().Format(time.DateOnly) + ") says it is done; both are live, so both are rendered and found; compare them with lore show --id " + f.ID + " and lore show --id " + f.By,
			Fix:     "when the newer row holds: lore " + noun + " archive --id " + f.ID})
	}
	return out
}
