// Package heldout is the held-out evaluation of lore ask (bug hunt H-237,
// devco D-978; Sync benchmark set 7, owner 2026-11-25: "stop fitting single
// questions"). The per-question fixes of sets 5 and 6 passed their own cases
// and missed the next blind set, while the recall gate stayed green. This
// package builds a synthetic store of the real shape (long as-built notes
// with many sections, ~150 short memories, hotfixes and decisions holding
// exact commands, flags, errors and defaults, code_repo cards whose names
// share common words, fact_* metadata, a second client's folder with
// overlapping words) from a fixed seed, dumps it for a question writer who
// never sees the ranking code, and scores lore ask on that writer's
// questions: "ask alone enough" = the answering row is shown and the answer
// sentence is inside what ask printed for it. The questions are split in two
// halves: a tune half kept in the repo (tune.jsonl) and a report half kept
// outside it, so a fix is judged on questions it was not fitted to.
//
// Every name in the store is made up.
package heldout

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// Folders of the store. The questions are asked from a code repo that
// attaches the whole store "kb", as the benchmark's answerer does.
const (
	StoreName   = "kb"
	AppFolder   = "kb/app"    // the product: long notes and the short rows
	OtherFolder = "kb/marlow" // another client: overlapping words, never the answer to an app question
	OrgFolder   = "kb/org"    // the code_repo resource cards
)

// DefaultSeed is the seed task bench:heldout and the dump use. The questions
// are written from the dump of this seed: changing it changes every row's
// text and invalidates the question files.
const DefaultSeed uint64 = 237

// RowKind is the lore command family that writes a row.
type RowKind string

// The row kinds of the store.
const (
	KindNote     RowKind = "architecturenote"
	KindMemory   RowKind = "memory"
	KindHotfix   RowKind = "hotfix"
	KindDecision RowKind = "decision"
)

// Meta keys every row carries. MetaKey is the stable key a question names
// its answer by, independent of the row ids lore gives at write time.
const (
	MetaKey      = "bench_key"
	MetaTopic    = "fact_topic"
	MetaState    = "fact_state"
	MetaVerified = "fact_verified_at"
)

// Meta is one metadata pair, kept in order so the batch file is stable.
type Meta struct{ Name, Value string }

// Row is one knowledge row of the store.
type Row struct {
	Key    string
	Kind   RowKind
	Folder string
	Title  string // empty for a memory (memories have no title)
	Body   string
	Meta   []Meta
}

// Text is what an answer can show of the row: its title and body.
func (r Row) Text() string {
	if r.Title == "" {
		return r.Body
	}
	return r.Title + "\n" + r.Body
}

// Card is one code_repo resource in OrgFolder. Its key is "card-<name>".
type Card struct {
	Key, Name, Purpose, Provider, URL string
}

// Shapes names the rows that carry each shape of sets 5-7, so the unit
// tests prove every shape is in the store and the report can say which
// shapes a question touches.
type Shapes struct {
	Conflicts [][2]string // two rows giving different values for one named setting
	Deep      []string    // long notes whose subject sentence sits in their last third
	Passing   []string    // short rows whose exact fact a long note also mentions in passing
	Siblings  [][2]string // short rows of two components with the same command shape
	Exact     []string    // short rows naming an exact command, flag, error or constant
}

// Store is the generated store.
type Store struct {
	Seed   uint64
	Rows   []Row
	Cards  []Card
	Shapes Shapes
}

// Row returns the row with the key, or false.
func (s *Store) Row(key string) (Row, bool) {
	for _, r := range s.Rows {
		if r.Key == key {
			return r, true
		}
	}
	return Row{}, false
}

// Card returns the card with the key, or false.
func (s *Store) Card(key string) (Card, bool) {
	for _, c := range s.Cards {
		if c.Key == key {
			return c, true
		}
	}
	return Card{}, false
}

// component is one made-up subsystem of the product: the generator writes
// its long note and its short rows from these words.
type component struct {
	slug    string   // the binary / cli name
	system  string   // what it is, in plain words
	purpose string   // one sentence on what it does
	things  []string // the nouns it keeps (each gets a table and a lifecycle)
	verbs   []string // its cli verbs (each gets a command and a worker job)
	errs    []string // error suffixes
	setting string   // the setting two rows contradict on
}

// components are the twelve subsystems. Names are invented; the systems are
// the kinds every product has, so their notes share the common words
// (service, worker, command, run, key, table, job, retry, file, deploy).
var components = []component{
	{"keymint", "auth service", "issues and checks sign-in tokens, sessions and signing keys", []string{"token", "session", "signing key"}, []string{"rotate", "revoke", "sweep"}, []string{"TOKEN_EXPIRED", "KEY_UNKNOWN"}, "session idle timeout"},
	{"quillq", "job queue", "stores jobs, hands out leases to workers and parks failed jobs as dead letters", []string{"job", "lease", "dead letter"}, []string{"drain", "requeue", "purge"}, []string{"LEASE_LOST", "PAYLOAD_TOO_LARGE"}, "lease renew interval"},
	{"hookwire", "webhook sender", "delivers signed webhook calls to partner endpoints and retries failed deliveries", []string{"delivery", "endpoint", "signature"}, []string{"replay", "pause", "verify"}, []string{"ENDPOINT_GONE", "SIGNATURE_MISMATCH"}, "delivery timeout"},
	{"crateload", "catalog import", "imports supplier catalog files into product rows through field mappings", []string{"import run", "product row", "mapping"}, []string{"import", "remap", "rollback"}, []string{"MAPPING_MISSING", "ROW_REJECTED"}, "import batch size"},
	{"binvault", "file storage", "keeps uploaded files as blobs and mirrors them to a second bucket", []string{"blob", "upload", "mirror"}, []string{"resync", "scrub", "drain"}, []string{"CHECKSUM_BAD", "MIRROR_STALE"}, "upload part size"},
	{"tickloom", "scheduler", "starts runs from cron-like schedules and records every trigger", []string{"schedule", "trigger", "run"}, []string{"trigger", "skip", "backfill"}, []string{"SCHEDULE_OVERLAP", "TRIGGER_LATE"}, "misfire grace"},
	{"feedsmith", "feed builder", "builds product feeds per sales channel and exports them as files", []string{"feed", "channel", "export"}, []string{"build", "publish", "diff"}, []string{"CHANNEL_REJECTED", "FEED_TOO_BIG"}, "export chunk size"},
	{"ledgerlink", "billing bridge", "copies invoices and payments between the product and the payment provider", []string{"invoice", "payment", "plan"}, []string{"reconcile", "refund", "resend"}, []string{"PLAN_MISMATCH", "PAYMENT_DUPLICATE"}, "reconcile window"},
	{"findex", "search indexer", "keeps the search index in step with the product rows", []string{"index", "document", "alias"}, []string{"reindex", "swap", "compact"}, []string{"ALIAS_TAKEN", "DOC_TOO_LARGE"}, "refresh interval"},
	{"pingpost", "notification sender", "sends email and push messages from templates", []string{"message", "template", "channel"}, []string{"send", "preview", "drain"}, []string{"TEMPLATE_MISSING", "RECIPIENT_BLOCKED"}, "send rate limit"},
	{"tracebook", "audit log", "records who changed what and exports the trail for reviews", []string{"event", "retention", "export"}, []string{"export", "prune", "verify"}, []string{"EVENT_OUT_OF_ORDER", "EXPORT_EXPIRED"}, "retention period"},
	{"gaterate", "rate limiter", "counts requests per key in buckets and enforces quotas", []string{"bucket", "quota", "override"}, []string{"reset", "grant", "sweep"}, []string{"QUOTA_EXCEEDED", "OVERRIDE_EXPIRED"}, "bucket refill period"},
}

// cards are the code_repo resources: their names and purposes share the
// question words of the notes (webhook, queue, import, file, schedule ...)
// without being the answer to a knowledge question.
var cards = []struct{ name, purpose string }{
	{"webhook_relay", "Relays incoming webhook calls to the worker queue; holds the relay service and its deploy files"},
	{"job_queue_ui", "Admin screens for the job queue: lists jobs, leases and dead letters and lets an admin requeue a job"},
	{"import_runner", "Runs catalog import jobs on a schedule and keeps the import run history"},
	{"file_gateway", "Serves uploaded files through signed links and caches them at the edge"},
	{"schedule_board", "Web board that shows every schedule, its next run and the last trigger"},
	{"feed_export", "Exports product feeds as files for each sales channel"},
	{"billing_sync", "Syncs invoices and payment status from the payment provider into the reporting tables"},
	{"auth_portal", "Sign-in pages, session cookies and the token refresh endpoint of the customer portal"},
	{"search_worker", "Worker that reads change events and updates the search index documents"},
	{"notify_hub", "Sends notification messages for the marketing team; templates live in the repo"},
	{"audit_viewer", "Read-only screens over the audit trail with filters by user and table"},
	{"rate_guard", "Edge proxy that enforces request quotas per api key"},
	{"catalog_admin", "Admin screens to edit product rows, mappings and categories"},
	{"token_tools", "Command line tools to inspect, sign and decode tokens during support work"},
	{"deploy_scripts", "Shared deploy scripts, config templates and the rollout command for every service"},
	{"worker_base", "Base image and run loop every background worker is built on: retry, backoff and graceful drain"},
	{"command_center", "Internal cli that wraps the run, drain and reindex commands of the services"},
	{"key_store", "Stores api keys and signing secrets for partner integrations"},
	{"run_history", "Keeps the history of every scheduled run and job run with its error and duration"},
	{"config_service", "Serves the config and default values of every service at start-up"},
}

// commonPool is filler with the store's common words: what makes a long
// note match every question about workers, commands, runs and keys.
var commonPool = []string{
	"Each worker reads its config at start-up and logs the run id of every job it takes.",
	"A failed job is retried with backoff; the worker keeps the error on the run row so the next run can read it.",
	"The service writes one row per run into its table, keyed by the tenant key and the run id.",
	"Every command prints the run it started and the worker that picked it up.",
	"Deploys go through the shared deploy scripts; a deploy restarts the workers one at a time.",
	"The config file names every default; an empty value in the config means the code default applies.",
	"Errors are returned with a stable code and logged with the request id, the key and the table.",
	"A retry never runs two copies of the same job: the lease on the job row guards it.",
	"Metrics are exported per worker: jobs taken, jobs failed, retries and the run duration.",
	"The admin screen lists each run with its status, the file it read and the key it used.",
	"The cli reads the same config as the service, so a command run by hand behaves like the worker.",
	"Tables are migrated by the deploy step before the new workers start.",
	"Each tenant key has its own rows; a worker never reads rows of another key.",
	"A run that hits an error keeps its partial rows and is marked failed for the next retry.",
	"Logs carry the command name, the worker name and the run id so a run can be followed end to end.",
	"Secrets come from the key store at start-up and are never written into the config file.",
}

// generator holds the random source and the store being built.
type generator struct {
	rng *rand.Rand
	s   *Store
}

// Generate builds the store for a seed. The same seed gives the same store,
// byte for byte (no map iteration, no clock).
func Generate(seed uint64) *Store {
	g := &generator{rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), s: &Store{Seed: seed}}
	for i, c := range components {
		g.component(i, c)
	}
	g.siblings()
	g.other()
	for _, c := range cards {
		g.s.Cards = append(g.s.Cards, Card{Key: "card-" + c.name, Name: c.name, Purpose: c.purpose, Provider: "gitforge", URL: "https://git.example.test/acme/" + c.name})
	}
	return g.s
}

func (g *generator) pick(xs []string) string { return xs[g.rng.IntN(len(xs))] }

func (g *generator) num(lo, hi int) int { return lo + g.rng.IntN(hi-lo+1) }

// verified is a fact_verified_at day in the autumn of 2026.
func (g *generator) verified() string {
	return fmt.Sprintf("2026-%02d-%02d", g.num(9, 11), g.num(1, 28))
}

func (g *generator) state() string {
	switch n := g.rng.IntN(20); {
	case n == 0:
		return "retired"
	case n < 3:
		return "planned"
	default:
		return "live"
	}
}

func (g *generator) add(r Row, topic string) {
	r.Meta = append([]Meta{{MetaKey, r.Key}, {MetaTopic, topic}, {MetaState, g.state()}, {MetaVerified, g.verified()}}, r.Meta...)
	g.s.Rows = append(g.s.Rows, r)
}

func upper(s string) string { return strings.ToUpper(strings.ReplaceAll(s, " ", "_")) }

// facts are one component's exact values, shared by its long note (some in
// passing) and its short rows.
type facts struct {
	cmds     []string // full command lines
	cmdSay   []string // what each command does
	consts   []string // CONSTANT = value
	errs     []string // error names
	errCause []string
	errFix   []string
	confVal  string // the setting's value one row gives (e.g. 5000 ms)
	deep     string // the subject sentence placed deep in the long note
	env      string
}

func (g *generator) facts(c component) facts {
	var f facts
	units := []string{"ms", "s", "min"}
	for i, v := range c.verbs {
		thing := c.things[i%len(c.things)]
		flag := []string{"--dry-run", "--tenant <key>", "--since <day>", "--limit <n>", "--force", "--batch <n>"}[g.rng.IntN(6)]
		f.cmds = append(f.cmds, fmt.Sprintf("%s %s %s", c.slug, v, flag))
		f.cmdSay = append(f.cmdSay, fmt.Sprintf("%ss every %s that is %s", v, thing, g.pick([]string{"stuck", "older than the cutoff", "marked failed", "waiting on a worker", "flagged by the last check"})))
	}
	for _, t := range c.things[:2] {
		f.consts = append(f.consts, fmt.Sprintf("%s_%s_MAX = %d", upper(c.slug), upper(t), g.num(2, 64)*50))
	}
	for _, e := range c.errs {
		f.errs = append(f.errs, fmt.Sprintf("E_%s_%s", upper(c.slug), e))
		f.errCause = append(f.errCause, g.pick([]string{"a worker restarted in the middle of a write", "the partner changed its endpoint without notice", "a config value was empty after a deploy", "two runs raced on the same row", "the clock of one host drifted by minutes", "a migration left an old column behind"}))
		f.errFix = append(f.errFix, g.pick([]string{"retry once with a fresh lease, then park it", "re-read the config before every run", "take the row lock before the write", "compare against the server clock, not the host clock", "drop the old column in the next migration", "page the on-call when it repeats three times in an hour"}))
	}
	f.confVal = fmt.Sprintf("%d %s", g.num(1, 60)*100, units[g.rng.IntN(len(units))])
	f.env = upper(c.slug) + "_" + upper(c.things[0]) + "_URL"
	f.deep = fmt.Sprintf("When a %s is %s during a deploy, %s keeps the old %s for %d minutes and then %ss it, so no worker sees a half-written %s.",
		c.things[0], g.pick([]string{"changed", "replaced", "moved", "renamed"}), c.slug, c.things[0], g.num(2, 30), c.verbs[0], c.things[1])
	return f
}

// component writes one component's long note and its thirteen short rows.
func (g *generator) component(i int, c component) {
	f := g.facts(c)
	key := func(kind string, n int) string { return fmt.Sprintf("app-%s-%s%02d", c.slug, kind, n) }
	g.note(key("note", 0), c, f, i)

	// exact commands
	for n := range c.verbs {
		k := key("m", n+1)
		g.add(Row{Key: k, Kind: KindMemory, Folder: AppFolder, Body: fmt.Sprintf("`%s` %s; it prints each %s it touched and exits non-zero when one fails.", f.cmds[n], f.cmdSay[n], c.things[n%len(c.things)])}, c.slug)
		g.s.Shapes.Exact = append(g.s.Shapes.Exact, k)
	}
	g.s.Shapes.Passing = append(g.s.Shapes.Passing, key("m", 1)) // the note's Commands section names cmds[0]
	// constants and env
	for n, k := range f.consts {
		rk := key("m", 4+n)
		g.add(Row{Key: rk, Kind: KindMemory, Folder: AppFolder, Body: fmt.Sprintf("%s caps how many %ss one run handles; raise it only together with the worker memory limit.", k, c.things[n])}, c.slug)
		g.s.Shapes.Exact = append(g.s.Shapes.Exact, rk)
	}
	g.add(Row{Key: key("m", 6), Kind: KindMemory, Folder: AppFolder, Body: fmt.Sprintf("%s must point at the internal address; the public address adds a second of latency per call.", f.env)}, c.slug)
	// hotfixes
	for n, e := range f.errs {
		rk := key("h", n+1)
		g.add(Row{Key: rk, Kind: KindHotfix, Folder: AppFolder, Title: fmt.Sprintf("%s after %s", e, f.errCause[n]), Body: fmt.Sprintf("%s showed up because %s. Fix: %s. Check the %s worker logs for the run id first.", e, f.errCause[n], f.errFix[n], c.slug)}, c.slug)
		g.s.Shapes.Exact = append(g.s.Shapes.Exact, rk)
	}
	// decisions
	g.add(Row{Key: key("d", 1), Kind: KindDecision, Folder: AppFolder, Title: fmt.Sprintf("%s keeps one %s per tenant", c.slug, c.things[1]), Body: fmt.Sprintf("We keep exactly one active %s per tenant key in %s. Why: %s.", c.things[1], c.slug, g.pick([]string{"two active ones made support guess which one a customer saw", "the partner bills per active one", "the cleanup job could not tell which one was current", "it halves the rows the worker scans per run"}))}, c.slug)
	g.add(Row{Key: key("d", 2), Kind: KindDecision, Folder: AppFolder, Title: fmt.Sprintf("%s runs as its own binary", c.slug), Body: fmt.Sprintf("%s is deployed as its own binary, not inside the api server. Why: %s.", c.slug, g.pick([]string{"a slow partner call must never hold an api request", "it needs a larger memory limit than the api", "it is restarted on its own schedule", "its deploys happen several times a day"}))}, c.slug)
	// the contradicting pair: an older memory gives a value, a newer hotfix says there is none
	a, b := key("m", 7), key("h", 3)
	g.add(Row{Key: a, Kind: KindMemory, Folder: AppFolder, Body: fmt.Sprintf("The %s %s defaults to %s; set %s_%s in the config to change it.", c.slug, c.setting, f.confVal, upper(c.slug), upper(c.setting))}, c.slug)
	g.add(Row{Key: b, Kind: KindHotfix, Folder: AppFolder, Title: fmt.Sprintf("%s %s has no default any more", c.slug, c.setting), Body: fmt.Sprintf("Since the October rewrite the %s %s has no default: an empty %s_%s stops the %s worker at start-up. Set it explicitly in every environment.", c.slug, c.setting, upper(c.slug), upper(c.setting), c.slug)}, c.slug)
	g.s.Shapes.Conflicts = append(g.s.Shapes.Conflicts, [2]string{a, b})
	// a short row of only common words: matches many questions, answers none
	g.add(Row{Key: key("m", 8), Kind: KindMemory, Folder: AppFolder, Body: fmt.Sprintf("The %s worker logs each run with its key and command; a failed run is retried by the same worker.", c.slug)}, c.slug)
}

// note writes a component's as-built note: 5-15 kB, many ## sections, the
// exact facts of some short rows in passing, a contradicting value in its
// config section for every third component, and the subject sentence in a
// section in its last third.
func (g *generator) note(key string, c component, f facts, i int) {
	var b strings.Builder
	sec := func(h string, paras ...string) {
		b.WriteString("## " + h + "\n\n")
		for _, p := range paras {
			b.WriteString(p + "\n\n")
		}
	}
	filler := func(n int) string {
		var p []string
		for range n {
			p = append(p, g.pick(commonPool))
		}
		return strings.Join(p, " ")
	}
	fmt.Fprintf(&b, "# As built: %s (%s)\n\n", c.slug, c.system)
	sec("Overview", fmt.Sprintf("%s is the %s of the product: it %s. It runs as a service with a pool of workers and a cli of the same name.", c.slug, c.system, c.purpose), filler(3))
	var tables []string
	for _, t := range c.things {
		tables = append(tables, fmt.Sprintf("- `%s_%ss`: one row per %s, keyed by tenant key and id, with status, created_at and the run id that last touched it.", c.slug, strings.ReplaceAll(t, " ", "_"), t))
	}
	sec("Data model", strings.Join(tables, "\n"), filler(2))
	sec("Request path", fmt.Sprintf("A call enters through the api, which validates the tenant key and writes a job; a %s worker takes the job, reads the %s row and writes the result.", c.slug, c.things[0]), filler(3))
	var jobs []string
	for n, v := range c.verbs {
		jobs = append(jobs, fmt.Sprintf("- %s job: %s.", v, f.cmdSay[n]))
	}
	sec("Workers and jobs", strings.Join(jobs, "\n"), filler(3))
	conf := []string{fmt.Sprintf("%s holds the address of the %s backend.", f.env, c.things[0]), filler(2)}
	if i%3 == 0 { // the note itself repeats the older value: a third source of the conflict
		conf = append(conf, fmt.Sprintf("The %s defaults to %s.", c.setting, f.confVal))
	}
	sec("Configuration and defaults", conf...)
	sec("Commands", fmt.Sprintf("The %s cli has one command per job; `%s` is the one run most often by hand.", c.slug, f.cmds[0]), filler(3))
	sec("Errors", fmt.Sprintf("Errors carry a stable code. %s means the %s could not be found or was too old; the worker parks the job.", f.errs[0], c.things[0]), filler(2))
	sec("Deploy and rollout", filler(5))
	sec("Metrics and alerts", fmt.Sprintf("%s exports jobs taken, jobs failed and the age of the oldest %s; an alert fires when the oldest is older than an hour.", c.slug, c.things[0]), filler(3))
	for _, t := range c.things {
		b.WriteString("### " + strings.ToUpper(t[:1]) + t[1:] + " lifecycle\n\n")
		b.WriteString(fmt.Sprintf("A %s is created by the api, picked up by a worker, and closed when its run ends. %s\n\n", t, filler(3)))
	}
	target := g.num(5200, 14000)
	for n := 1; b.Len() < target*2/3; n++ {
		sec(fmt.Sprintf("Operations notes %d", n), filler(4), filler(3))
	}
	// the subject sentence: deep, in a section of its own among filler
	sec("Deploy-time "+c.things[0]+" changes", filler(3), f.deep, filler(2))
	g.s.Shapes.Deep = append(g.s.Shapes.Deep, key)
	for n := 1; b.Len() < target; n++ {
		sec(fmt.Sprintf("Known limits %d", n), filler(5))
	}
	sec("History", fmt.Sprintf("2026-%02d: moved to its own binary. 2026-%02d: the %s table gained the run id.", g.num(3, 6), g.num(7, 10), c.things[0]))
	g.add(Row{Key: key, Kind: KindNote, Folder: AppFolder, Title: fmt.Sprintf("As built: %s (%s)", c.slug, c.system), Body: strings.TrimSpace(b.String())}, c.slug)
}

// siblings writes, for pairs of components that share a verb, one more row
// each with the same command shape, so "which command drains X" has a
// sibling "which command drains Y".
func (g *generator) siblings() {
	pairs := [][2]int{{1, 4}, {4, 9}, {0, 11}}
	for n, p := range pairs {
		var keys [2]string
		for j, ci := range p {
			c := components[ci]
			k := fmt.Sprintf("app-%s-s%02d", c.slug, n+1)
			v := sharedVerb(components[p[0]], components[p[1]])
			g.add(Row{Key: k, Kind: KindMemory, Folder: AppFolder, Body: fmt.Sprintf("To %s %s before a deploy run `%s %s --wait %ds`; it returns once no worker holds a %s.", v, c.slug, c.slug, v, g.num(2, 12)*10, c.things[0])}, c.slug)
			keys[j] = k
		}
		g.s.Shapes.Siblings = append(g.s.Shapes.Siblings, keys)
	}
}

func sharedVerb(a, b component) string {
	for _, v := range a.verbs {
		for _, w := range b.verbs {
			if v == w {
				return v
			}
		}
	}
	return a.verbs[0]
}

// other writes the second client's folder: two long notes and short rows
// using the same words as the app (queue, webhook, import, retry, default)
// about that client's own systems.
func (g *generator) other() {
	for n, sys := range []string{"order queue", "partner webhooks"} {
		var b strings.Builder
		fmt.Fprintf(&b, "# As built: marlow %s\n\n", sys)
		for s := 1; b.Len() < g.num(5000, 7000); s++ {
			fmt.Fprintf(&b, "## Section %d\n\n", s)
			for range 6 {
				b.WriteString(g.pick(commonPool) + " ")
			}
			b.WriteString("\n\n")
		}
		g.add(Row{Key: fmt.Sprintf("oth-note%02d", n+1), Kind: KindNote, Folder: OtherFolder, Title: "As built: marlow " + sys, Body: strings.TrimSpace(b.String())}, "marlow")
	}
	subjects := []string{"order queue", "partner webhook", "catalog import", "file upload", "nightly schedule", "invoice export"}
	for n := range 24 {
		s := subjects[n%len(subjects)]
		k := fmt.Sprintf("oth-r%02d", n+1)
		switch n % 3 {
		case 0:
			g.add(Row{Key: k, Kind: KindMemory, Folder: OtherFolder, Body: fmt.Sprintf("Marlow's %s retries a failed job %d times; its default timeout is %d s.", s, g.num(2, 9), g.num(5, 120))}, "marlow")
		case 1:
			g.add(Row{Key: k, Kind: KindHotfix, Folder: OtherFolder, Title: fmt.Sprintf("Marlow %s stuck after deploy", s), Body: fmt.Sprintf("The marlow %s worker stopped taking jobs after a deploy; restart it with the drain command and check the key in the config.", s)}, "marlow")
		default:
			g.add(Row{Key: k, Kind: KindDecision, Folder: OtherFolder, Title: fmt.Sprintf("Marlow keeps its own %s", s), Body: fmt.Sprintf("Marlow runs its own %s service instead of ours. Why: their contract keeps their data in their own account.", s)}, "marlow")
		}
	}
}
