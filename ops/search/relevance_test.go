package search_test

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/schema"
)

// relevanceRow is one row of the relevance store: its table and fields.
type relevanceRow struct {
	key    string
	table  row.Table
	fields map[string]string
}

// relevanceRows is a small made-up company store (product "orbit", client
// "Pinecrest") across several kinds.
var relevanceRows = []relevanceRow{
	{"decDeployProd", schema.TableDecisions, map[string]string{"title": "deploy.prod.yml auto-deploys on push to main", "body": "Every push to main ships orbit to production through the pipeline."}},
	{"decLath", schema.TableDecisions, map[string]string{"title": "Orbit deploys with lath", "body": "Run lath run deploy staging to ship orbit to the staging server; production needs a release tag."}},
	{"decChecklist", schema.TableDecisions, map[string]string{"title": "Deployment checklist", "body": "Tag the release and announce it."}},
	{"decPostgres", schema.TableDecisions, map[string]string{"title": "Use Postgres for orbit", "body": "We chose Postgres over MySQL for its JSON support."}},
	{"ruleLocal", schema.TableRules, map[string]string{"body": "Run task dev to start orbit locally; it reads .env.local."}},
	{"ruleCommit", schema.TableRules, map[string]string{"body": "Commit subjects follow the conventional format; the sync worker rejects the others."}},
	{"memStaging", schema.TableMemories, map[string]string{"body": "The Pinecrest staging server lives in the ops store; ask the lead for access."}},
	{"memInvoice", schema.TableMemories, map[string]string{"body": "Pinecrest invoices go out on the first Monday of the month."}},
	{"memBackups", schema.TableMemories, map[string]string{"body": "Nightly backups of every database are kept for 30 days."}},
	{"pbkLocal", schema.TablePlaybooks, map[string]string{"name": "Local development setup", "body": "Install Go, copy .env.example to .env.local, then start the server locally with task dev."}},
	{"pbkRestore", schema.TablePlaybooks, map[string]string{"name": "Restore the Pinecrest database", "body": "Download the nightly backup and run task restore-db."}},
	{"hfxWorker", schema.TableHotfixes, map[string]string{"title": "Sync worker does not hot-reload", "body": "Restart the worker after changing its config."}},
	{"taskRotate", schema.TableTasks, map[string]string{"title": "Rotate the Pinecrest database password"}},
	{"incOutage", schema.TableIncidents, map[string]string{"title": "Orbit outage after a failed migration", "body": "The migration locked the users table for ten minutes."}},
	{"arcBilling", schema.TableArchitectureNotes, map[string]string{"title": "Orbit talks to the billing service over NATS", "body": "Events go through one subject per tenant."}},
	// devco:D-1009: rows naming a code identifier in camelCase
	{"memCronJobs", schema.TableMemories, map[string]string{"body": "The cron binary starts every job named in its enabledJobs list."}},
	{"ruleCallback", schema.TableRules, map[string]string{"body": "Webhook replies go to the callbackHost each app sets."}},
}

// relevanceCases are the guard of D-435/D-436: a plain question finds its
// rows (stop words out, any word matches, word forms match), and the best
// row comes first across kinds. first is the hit that must be first ("" =
// any); top must all be among the first three; never must not be listed.
var relevanceCases = []struct {
	query string
	first string
	top   []string
	never []string
}{
	{query: "how do we deploy orbit", first: "decLath", top: []string{"decDeployProd"}},
	{query: "where is the Pinecrest staging server", first: "memStaging"},
	{query: "how do I run orbit locally", first: "ruleLocal"},
	{query: "run locally", first: "ruleLocal", top: []string{"pbkLocal"}},
	{query: "local development", first: "pbkLocal"},
	{query: "deployment", first: "decChecklist", top: []string{"decLath", "decDeployProd"}},
	{query: "deploying to staging", first: "decLath"},
	{query: "who handles the pinecrest invoices", first: "memInvoice"},
	{query: "rotate database password", first: "taskRotate"},
	{query: "restore the pinecrest database", first: "pbkRestore", top: []string{"taskRotate"}},
	{query: "why did we pick postgres", first: "decPostgres"},
	{query: "orbit outage", first: "incOutage"},
	{query: "failed migrations", first: "incOutage"},
	{query: "billing service", first: "arcBilling"},
	{query: "how long are backups kept", first: "memBackups"},
	{query: "sync worker reload", first: "hfxWorker", top: []string{"ruleCommit"}},
	{query: "commit subject format", first: "ruleCommit"},
	{query: "nightly backup", top: []string{"memBackups", "pbkRestore"}},
	{query: "the staging server", top: []string{"memStaging", "decLath"}},
	{query: "deploy", top: []string{"decLath", "decDeployProd", "decChecklist"}},
	{query: "deploy -lath", top: []string{"decDeployProd", "decChecklist"}, never: []string{"decLath"}},
	{query: "Pinecrest", top: []string{"taskRotate", "pbkRestore"}},
	// D-468: a prefix query ranks title hits like a word query does
	{query: "checkl*", first: "decChecklist"},
	{query: "datab*", top: []string{"taskRotate", "pbkRestore"}},
	{query: "orbi* outage", first: "incOutage"},
	// devco:D-1009 (bug hunt H-271): an identifier written with _ - or .
	// finds the row writing it in camelCase (the index reads enabledJobs
	// as one word, enabled_jobs as two)
	{query: "enabled_jobs", first: "memCronJobs"},
	{query: "which jobs are in enabled-jobs", first: "memCronJobs"},
	{query: "callback_host", first: "ruleCallback"},
	{query: "callback.host", first: "ruleCallback"},
}

// seedRelevance writes relevanceRows and returns key -> id and id -> key.
func seedRelevance(t *testing.T, h *opstest.H) (map[string]string, map[string]string) {
	t.Helper()
	byKey, byID := map[string]string{}, map[string]string{}
	for _, r := range relevanceRows {
		set := map[string]string{}
		for f, v := range r.fields {
			set[f] = strconv.Quote(v)
		}
		if r.table == schema.TablePlaybooks {
			set["project_id"] = strconv.Quote(h.Project) // optional there; a default search reads the project's rows
		}
		id := h.Seed(r.table, set)
		byKey[r.key], byID[id] = id, r.key
	}
	return byKey, byID
}

// searchHits runs search.run and decodes its hits.
func searchHits(t *testing.T, h *opstest.H, query string) []search.Hit {
	t.Helper()
	in, err := json.Marshal(search.RunIn{Query: query})
	if err != nil {
		t.Fatal(err)
	}
	var out search.RunOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(search.OpRun, string(in)))), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data.Hits
}

// TestSearchRelevance (D-435, D-436): every question of the relevance set
// finds its expected rows first. Before D-435 the natural-language ones
// found nothing (every word, stop words too, had to match) and the order
// interleaved one hit per kind.
func TestSearchRelevance(t *testing.T) {
	h := opstest.New(t)
	_, byID := seedRelevance(t, h)
	for _, c := range relevanceCases {
		hits := searchHits(t, h, c.query)
		keys := make([]string, len(hits))
		for i, x := range hits {
			keys[i] = byID[x.ID]
		}
		if len(keys) == 0 {
			t.Errorf("%q: no hits", c.query)
			continue
		}
		if c.first != "" && keys[0] != c.first {
			t.Errorf("%q: first %s, want %s (order %v)", c.query, keys[0], c.first, keys)
		}
		top := keys[:min(3, len(keys))]
		for _, k := range c.top {
			if !slices.Contains(top, k) {
				t.Errorf("%q: %s not in the top 3 %v (order %v)", c.query, k, top, keys)
			}
		}
		for _, k := range c.never {
			if slices.Contains(keys, k) {
				t.Errorf("%q: %s listed (order %v)", c.query, k, keys)
			}
		}
		for i := 1; i < len(hits); i++ {
			if hits[i].Score > hits[i-1].Score {
				t.Errorf("%q: hit %d scores %.3f above hit %d's %.3f: not best first", c.query, i, hits[i].Score, i-1, hits[i-1].Score)
			}
		}
	}
}

// TestSearchPartialMatches (D-435): when no row holds every word, the rows
// holding most of them are still listed, marked partial, and the terminal
// says no row holds every word; a row holding every word is never
// partial and outranks every partial one.
func TestSearchPartialMatches(t *testing.T) {
	h := opstest.New(t)
	byKey, _ := seedRelevance(t, h)
	hits := searchHits(t, h, "deploy pinecrest")
	if len(hits) == 0 {
		t.Fatal("no hits for a query no row holds whole")
	}
	for _, x := range hits {
		if !x.Partial {
			t.Errorf("hit %s not partial", x.ID)
		}
	}
	text := opstest.Text(h.Must(search.OpRun, `{"query":"deploy pinecrest"}`))
	if !strings.Contains(text, "No row holds every word") {
		t.Errorf("no partial note:\n%s", text)
	}
	hits = searchHits(t, h, "staging server pinecrest")
	if len(hits) < 2 || hits[0].ID != byKey["memStaging"] || hits[0].Partial || !hits[1].Partial || hits[0].Score <= hits[1].Score {
		t.Fatalf("full match not first and alone unmarked: %+v", hits)
	}
}

// TestSearchScoresSpread (D-436): scores are a relevance in (0, 1), not
// 1/(60+rank): a title hit holding every word scores well above a body
// hit holding one.
func TestSearchScoresSpread(t *testing.T) {
	h := opstest.New(t)
	byKey, _ := seedRelevance(t, h)
	hits := searchHits(t, h, "orbit deploys")
	if len(hits) < 2 || hits[0].ID != byKey["decLath"] {
		t.Fatalf("order: %+v", hits)
	}
	last := hits[len(hits)-1]
	if hits[0].Score < 0.8 || hits[0].Score >= 1 || hits[0].Score-last.Score < 0.2 {
		t.Fatalf("scores %.3f .. %.3f: want a spread from about 1 down", hits[0].Score, last.Score)
	}
	if hits[0].Title != "Orbit deploys with lath" {
		t.Errorf("title %q", hits[0].Title)
	}
}
