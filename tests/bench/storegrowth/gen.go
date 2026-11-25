// Package storegrowth is the store growth bench (spec item 1, devco
// D-989): a generator for a synthetic team store of the real shape at
// 1k / 100k / 1M commits, and a timing harness that runs lore against it
// (harness_test.go, build tag bench, task bench:store).
//
// The generator needs no lore binary: it writes a `git fast-import`
// stream that continues a seed history (a store lore itself made: the
// store marker, the org file, the roster) with N commits of the shape a
// busy team store has, so 1M commits take minutes and no lore process per
// commit. The row files are copies of row files a real lore wrote (the
// harness passes them as Templates), so the format is always current.
//
//	seed tip ─▶ Generate(plan) ─▶ fast-import stream ─▶ git fast-import
//	  per commit (seeded rand, fixed order):
//	  98.5%  one row file: new (70%), edited (27%) or deleted (3%)
//	   0.5%  a bulk commit of 20-200 new rows (an import)
//	     1%  a landing: a side commit with a few rows, merged --no-ff
//	  (the spec's draft said 4% bulk; that is 5.6 rows per commit, and
//	  the measured stores change about one file per commit and hold
//	  fewer rows than commits, so bulk is 0.5%: about 1.3 rows a commit)
//	  every OrgEvery commits: the admin edits lore-org.yaml
//	  at the middle commit: a non-admin edits lore-org.yaml and makes
//	  themself admin in their own stint (both must be held back)
//	  at the end: refs/lore/signals/main, 15 layer refs, 30 layer-trash refs
//
// The parts kept small on purpose: ids, times and text come from the row
// counter and the seed, so a run holds only two small ints per row.
package storegrowth

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

// Person is one synthetic teammate the generator commits as.
type Person struct {
	Name    string // git author name
	Email   string // git author email (the roster's stable_key is human:<Email>)
	ActorID string // their actor id on the seed's roster (rows state it as the writer)
	Admin   bool   // an org admin (the org file changes are theirs)
}

// Plan is everything a generated history depends on: the same Plan gives
// the same stream byte for byte.
type Plan struct {
	Commits int   // commits after the seed (not counting side commits of landings)
	Seed    int64 // rand seed
	// Start is the author time of the first generated commit; Step the
	// time between two commits (a busy store: about 3 minutes).
	Start time.Time
	Step  time.Duration
	// People commit in turn; People[0] must be an admin. AgentActorID, when
	// set, is an agent session the first person runs: a fifth of their rows
	// state it as the writer (an agent's write, committed as its person).
	People       []Person
	AgentActorID string
	// Templates maps a table (memories, tasks, ...) to one row file lore
	// wrote for it; only these tables get rows.
	Templates map[string][]byte
	// Folders are the store folders rows go to, in order: a folder takes
	// RowsPerFolder new rows, then the next one starts (a team adds
	// projects as it grows); the last folder takes the rest.
	Folders       []string
	RowsPerFolder int
	// OrgFile is the seed's org file path and content; OrgEvery how many
	// commits apart the admin changes it.
	OrgFilePath string
	OrgFile     []byte
	OrgEvery    int
	// Promotion is a stint row of People[1] at the seed (path and content):
	// at the middle commit People[1] rewrites it with org_role admin, the
	// roster change the authority replay must hold back.
	PromotionPath string
	Promotion     []byte
	// Branch is the store branch the stream continues ("main").
	Branch string
	// Tail makes a continuation of a generated store (the harness's "100
	// new remote commits"): rows only, no org change, no held pair and no
	// extra refs. Give it another Seed and a Start after the last commit,
	// so its ids are new.
	Tail bool
}

// Stats counts what a stream wrote, for the shape test and the report.
type Stats struct {
	Commits      int            // every commit on the branch (merges and org changes included)
	SideCommits  int            // commits only on landing side branches
	Merges       int            // landing merges
	Bulk         int            // bulk commits
	OrgChanges   int            // lore-org.yaml changes (admin and non-admin)
	HeldMessages []string       // subjects of the commits the authority must hold back
	Rows         map[string]int // live rows per table at the end
	Deleted      int            // rows deleted
	Edits        int            // row edits
	Refs         []string       // refs written besides the branch
}

// Weight of each table: rows per 100 new rows, scaled from the two
// busiest measured stores (spec section 1: comments, tasks and memories
// are the big folders, decisions next).
var tableWeight = map[string]int{
	"memories":  33,
	"comments":  25,
	"tasks":     17,
	"decisions": 10,
	"rules":     6,
	"hotfixes":  5,
	"playbooks": 4,
}

// adminTables are written by an admin only: lore holds back a must rule
// or a high hotfix in the company folder from anyone else, and the
// template rows are such, so a member's would be a held change the plan
// did not ask for.
var adminTables = map[string]bool{"rules": true, "hotfixes": true}

// errPlan is a plan the generator cannot run.
var errPlan = errors.New("storegrowth: plan")

// row is one generated row: its table, its global sequence (id, folder and
// creation time derive from it) and whether it is deleted.
type row struct {
	table   string
	seq     uint32
	created uint32 // commit index of its creation
	gone    bool
}

// gen is one stream being written.
type gen struct {
	p       Plan
	w       *bufio.Writer
	r       *rand.Rand
	tables  []string // tables with templates, sorted
	weights []int    // cumulative weights, parallel to tables
	tpl     map[string]map[string]json.RawMessage
	rows    []row
	nextSeq uint32
	mark    int
	tip     int      // mark of the branch's newest commit
	repos   []string // client repo patterns the org file's changes added
	commit  int      // index of the commit being written (0-based)
	st      Stats
}

// Validate reports the first thing wrong with p.
func (p Plan) Validate() error {
	switch {
	case p.Commits < 1:
		return fmt.Errorf("%w: commits must be at least 1", errPlan)
	case len(p.People) < 2 || !p.People[0].Admin || p.People[1].Admin:
		return fmt.Errorf("%w: people[0] must be an admin and people[1] not", errPlan)
	case len(p.Templates) == 0:
		return fmt.Errorf("%w: no row templates", errPlan)
	case len(p.Folders) == 0 || p.RowsPerFolder < 1:
		return fmt.Errorf("%w: folders and rows per folder", errPlan)
	case p.OrgFilePath == "" || len(p.OrgFile) == 0 || p.OrgEvery < 1:
		return fmt.Errorf("%w: org file and its interval", errPlan)
	case strings.Contains("\n"+string(p.OrgFile), "\nrepos:"):
		return fmt.Errorf("%w: the seed's org file must have no repos: list (the changes write it)", errPlan)
	case p.PromotionPath == "" || !strings.Contains(string(p.Promotion), `"org_role": "member"`):
		return fmt.Errorf("%w: the promotion stint must hold \"org_role\": \"member\"", errPlan)
	case p.Branch == "":
		return fmt.Errorf("%w: branch", errPlan)
	}
	for t := range p.Templates {
		if tableWeight[t] == 0 {
			return fmt.Errorf("%w: no weight for table %s", errPlan, t)
		}
	}
	return nil
}

// Generate writes the fast-import stream of p to w. The stream continues
// the branch's current tip (fast-import's `from <branch>^0`), so it is fed
// to a repository that holds the seed.
func Generate(w io.Writer, p Plan) (Stats, error) {
	if err := p.Validate(); err != nil {
		return Stats{}, err
	}
	g := &gen{p: p, w: bufio.NewWriterSize(w, 1<<20), r: rand.New(rand.NewPCG(uint64(p.Seed), 0x5eed))}
	g.st.Rows = map[string]int{}
	g.tpl = map[string]map[string]json.RawMessage{}
	for t, b := range p.Templates {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			return Stats{}, fmt.Errorf("storegrowth: template %s: %w", t, err)
		}
		g.tpl[t] = m
		g.tables = append(g.tables, t)
	}
	slices.Sort(g.tables)
	sum := 0
	for _, t := range g.tables {
		sum += tableWeight[t]
		g.weights = append(g.weights, sum)
	}
	if err := g.run(); err != nil {
		return Stats{}, err
	}
	return g.st, g.w.Flush()
}

// run writes every commit, then the extra refs.
func (g *gen) run() error {
	first := true
	held := g.p.Commits / 2
	for g.commit = 0; g.commit < g.p.Commits; g.commit++ {
		from := ""
		if first {
			from = g.p.Branch + "^0"
			first = false
		}
		var err error
		switch k := g.r.IntN(1000); {
		case g.commit == held && !g.p.Tail:
			err = g.heldCommits(from)
		case g.commit > 0 && g.commit%g.p.OrgEvery == 0 && !g.p.Tail:
			err = g.orgChange(from, g.p.People[0], "admin")
		case k < 10 && g.commit > 0:
			err = g.landing()
		case k < 15:
			err = g.bulk(from)
		default:
			err = g.single(from)
		}
		if err != nil {
			return err
		}
	}
	for _, r := range g.rows { // live rows on the branch (the extra refs' rows are not)
		if !r.gone {
			g.st.Rows[r.table]++
		}
	}
	if g.p.Tail {
		return nil
	}
	return g.extraRefs()
}

// author is who makes commit i (people in turn, a little shuffled).
func (g *gen) author() Person {
	return g.p.People[(g.commit+g.r.IntN(2))%len(g.p.People)]
}

// when is commit i's time.
func (g *gen) when() time.Time {
	return g.p.Start.Add(time.Duration(g.commit) * g.p.Step)
}

// pickTable draws a table by weight among those a may write.
func (g *gen) pickTable(a Person) string {
	for {
		n := g.r.IntN(g.weights[len(g.weights)-1])
		i, _ := slices.BinarySearch(g.weights, n+1)
		if a.Admin || !adminTables[g.tables[i]] || len(g.tables) == 1 {
			return g.tables[i]
		}
	}
}

// prefix is the id prefix of a table's template (mem, tsk, ...).
func (g *gen) prefix(table string) string {
	var id string
	_ = json.Unmarshal(g.tpl[table]["id"], &id)
	if p, _, ok := strings.Cut(id, "_"); ok {
		return p
	}
	return table[:3]
}

// rowID is the id of row seq: the template's prefix and 32 hex digits
// (a v7-like time part from the creation, then the sequence).
func (g *gen) rowID(table string, seq uint32, created time.Time) string {
	return fmt.Sprintf("%s_%012x7%03x8%015x", g.prefix(table), created.UnixMilli(), seq&0xfff, uint64(seq)^uint64(g.p.Seed))
}

// folder is the folder of row seq.
func (g *gen) folder(seq uint32) string {
	i := int(seq) / g.p.RowsPerFolder
	if i >= len(g.p.Folders) {
		i = len(g.p.Folders) - 1
	}
	return g.p.Folders[i]
}

// path is the row file of r.
func (g *gen) path(r row) string {
	return g.folder(r.seq) + "/" + r.table + "/" + g.rowID(r.table, r.seq, g.timeOf(r.created)) + ".json"
}

// timeOf is the time of commit index i.
func (g *gen) timeOf(i uint32) time.Time {
	return g.p.Start.Add(time.Duration(i) * g.p.Step)
}

// writer is the actor id a row by person a states: the agent session for
// a fifth of the admin's rows, else the person.
func (g *gen) writer(a Person) string {
	if a.Admin && g.p.AgentActorID != "" && g.r.IntN(5) == 0 {
		return g.p.AgentActorID
	}
	return a.ActorID
}

// words feed the generated bodies: enough variety for search to rank.
var words = strings.Fields(`deploy invoice billing release cache index schema
migration rollback customer webhook queue retry timeout token session
report export import payment refund order cart search ranking backup
restore staging production monitor alert latency budget roadmap review
onboarding contract vendor audit backlog sprint incident postmortem`)

// text is a short deterministic sentence for row seq, revision rev.
func (g *gen) text(seq uint32, rev int) string {
	var b strings.Builder
	n := 6 + int(seq%9)
	for i := range n {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(words[(int(seq)*7+i*13+rev)%len(words)])
	}
	fmt.Fprintf(&b, " (note %d", seq)
	if rev > 0 {
		fmt.Fprintf(&b, ", revision %d", rev)
	}
	b.WriteString(")")
	return b.String()
}

// rowJSON is row r's file content as of revision rev, written by actor.
func (g *gen) rowJSON(r row, rev int, actor string) ([]byte, error) {
	m := map[string]json.RawMessage{}
	for k, v := range g.tpl[r.table] {
		m[k] = v
	}
	created, now := g.timeOf(r.created), g.when()
	set := func(k string, v string) {
		if _, ok := m[k]; ok || k == "id" || k == "updated_by" || k == "updated_at" {
			b, _ := json.Marshal(v)
			m[k] = b
		}
	}
	id := g.rowID(r.table, r.seq, created)
	set("id", id)
	set("_hlc", now.UTC().Format("2006-01-02T15:04:05.000Z")+"/0000/"+actor)
	set("created_at", created.UTC().Format("2006-01-02T15:04:05.000000000Z"))
	set("updated_at", now.UTC().Format("2006-01-02T15:04:05.000000000Z"))
	set("tx_at", now.UTC().Format(time.RFC3339Nano))
	if rev == 0 {
		set("created_by", actor)
		set("created_by_actor_id", actor)
	}
	set("updated_by", actor)
	txt := g.text(r.seq, rev)
	set("body", txt)
	set("title", txt[:min(len(txt), 48)])
	set("name", txt[:min(len(txt), 48)])
	delete(m, "_o") // the template's origin names the seed's repo and commit
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("storegrowth: row %s: %w", id, err)
	}
	return append(b, '\n'), nil
}

// newRow adds a row of table and returns it.
func (g *gen) newRow(table string) row {
	r := row{table: table, seq: g.nextSeq, created: uint32(g.commit)}
	g.nextSeq++
	g.rows = append(g.rows, r)
	return r
}

// liveRow picks an existing row (recent ones more often, as people edit
// what they just wrote); ok is false when none is live.
func (g *gen) liveRow() (int, bool) {
	n := len(g.rows)
	for range 8 {
		if n == 0 {
			return 0, false
		}
		var i int
		if g.r.IntN(2) == 0 {
			i = n - 1 - g.r.IntN(min(n, 200))
		} else {
			i = g.r.IntN(n)
		}
		if !g.rows[i].gone {
			return i, true
		}
	}
	return 0, false
}

// file is one file change of a commit.
type file struct {
	path string
	data []byte // nil deletes
}

// begin writes a commit header.
func (g *gen) begin(ref, from string, a Person, msg string, merge string) {
	g.mark++
	ts := g.when().Unix()
	fmt.Fprintf(g.w, "commit %s\nmark :%d\nauthor %s <%s> %d +0000\ncommitter %s <%s> %d +0000\ndata %d\n%s\n",
		ref, g.mark, a.Name, a.Email, ts, a.Name, a.Email, ts, len(msg), msg)
	if from != "" {
		fmt.Fprintf(g.w, "from %s\n", from)
	}
	if merge != "" {
		fmt.Fprintf(g.w, "merge %s\n", merge)
	}
}

// files writes a commit's file changes and ends it.
func (g *gen) files(fs []file) {
	for _, f := range fs {
		if f.data == nil {
			fmt.Fprintf(g.w, "D %s\n", f.path)
			continue
		}
		fmt.Fprintf(g.w, "M 100644 inline %s\ndata %d\n", f.path, len(f.data))
		_, _ = g.w.Write(f.data)
		_ = g.w.WriteByte('\n')
	}
	_ = g.w.WriteByte('\n')
}

// commitOnBranch writes one commit on the branch.
func (g *gen) commitOnBranch(from string, a Person, msg string, fs []file) {
	g.begin("refs/heads/"+g.p.Branch, from, a, msg, "")
	g.files(fs)
	g.tip = g.mark
	g.st.Commits++
}

// branchTip is the from of a commit that forks off the branch's newest
// commit: its mark, or the seed tip before any commit was written.
func (g *gen) branchTip() string {
	if g.tip == 0 {
		return "refs/heads/" + g.p.Branch + "^0"
	}
	return fmt.Sprintf(":%d", g.tip)
}

// single is the common commit: one row added, edited or deleted.
func (g *gen) single(from string) error {
	a := g.author()
	k := g.r.IntN(100)
	if i, ok := g.liveRow(); ok && k >= 70 {
		r := g.rows[i]
		if adminTables[r.table] && !a.Admin {
			a = g.p.People[0]
		}
		if k >= 97 {
			g.rows[i].gone = true
			g.st.Deleted++
			g.commitOnBranch(from, a, fmt.Sprintf("lore: %s delete %s", singular(r.table), g.rowID(r.table, r.seq, g.timeOf(r.created))), []file{{path: g.path(r)}})
			return nil
		}
		data, err := g.rowJSON(r, g.commit, g.writer(a))
		if err != nil {
			return err
		}
		g.st.Edits++
		g.commitOnBranch(from, a, fmt.Sprintf("lore: %s edit %s", singular(r.table), g.rowID(r.table, r.seq, g.timeOf(r.created))), []file{{path: g.path(r), data: data}})
		return nil
	}
	r := g.newRow(g.pickTable(a))
	data, err := g.rowJSON(r, 0, g.writer(a))
	if err != nil {
		return err
	}
	g.commitOnBranch(from, a, fmt.Sprintf("lore: %s add %s", singular(r.table), g.rowID(r.table, r.seq, g.timeOf(r.created))), []file{{path: g.path(r), data: data}})
	return nil
}

// newFiles makes n new rows by a.
func (g *gen) newFiles(n int, a Person) ([]file, error) {
	fs := make([]file, 0, n)
	for range n {
		r := g.newRow(g.pickTable(a))
		data, err := g.rowJSON(r, 0, g.writer(a))
		if err != nil {
			return nil, err
		}
		fs = append(fs, file{path: g.path(r), data: data})
	}
	return fs, nil
}

// bulk is an import: 20-200 new rows in one commit.
func (g *gen) bulk(from string) error {
	a := g.author()
	n := 20 + g.r.IntN(181)
	fs, err := g.newFiles(n, a)
	if err != nil {
		return err
	}
	g.st.Bulk++
	g.commitOnBranch(from, a, fmt.Sprintf("lore: batch apply (%d rows)", n), fs)
	return nil
}

// SideRef is the scratch ref landings' side commits are written on; the
// caller deletes it after the import (git update-ref -d).
const SideRef = "refs/lore/gen/side"

// landing is a layer landing: a side commit with 2-6 rows off the
// branch, merged back with a merge commit (fast-import does not merge:
// the merge commit carries the side's files itself, so its tree is the
// side's).
func (g *gen) landing() error {
	a := g.author()
	fs, err := g.newFiles(2+g.r.IntN(5), a)
	if err != nil {
		return err
	}
	g.begin(SideRef, g.branchTip(), a, "lore: layer commit", "")
	g.files(fs)
	sideMark := g.mark
	g.st.SideCommits++
	g.begin("refs/heads/"+g.p.Branch, "", a, "lore: land layer into "+g.p.Branch, fmt.Sprintf(":%d", sideMark))
	g.files(fs)
	g.tip = g.mark
	g.st.Commits++
	g.st.Merges++
	return nil
}

// orgFile is the org file with the repos: patterns so far plus extra.
// Each change adds a client pattern: lore accepts a change that leaves the
// parsed file the same (a comment, a reformat) from anyone, so only a real
// change exercises the admin check.
func (g *gen) orgFile(extra string) []byte {
	b := slices.Clone(g.p.OrgFile)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	pats := g.repos
	if extra != "" {
		pats = append(slices.Clone(pats), extra)
	}
	if len(pats) == 0 {
		return b
	}
	b = append(b, "repos:\n"...)
	for _, p := range pats {
		b = append(b, "    - "+p+"\n"...)
	}
	return b
}

// orgChange is the admin adding a client repo pattern to the org file.
func (g *gen) orgChange(from string, a Person, _ string) error {
	pat := fmt.Sprintf("github.com/client-%d/*", g.commit)
	g.commitOnBranch(from, a, "lore: org config set repos", []file{{path: g.p.OrgFilePath, data: g.orgFile(pat)}})
	g.repos = append(g.repos, pat)
	g.st.OrgChanges++
	return nil
}

// heldCommits are the two changes the authority must hold back: a
// non-admin's org file edit and their self-promotion to admin.
func (g *gen) heldCommits(from string) error {
	member := g.p.People[1]
	org := g.orgFile(fmt.Sprintf("github.com/held-%d/*", g.commit))
	g.commitOnBranch(from, member, "org file fix", []file{{path: g.p.OrgFilePath, data: org}})
	g.st.OrgChanges++
	promoted := strings.Replace(string(g.p.Promotion), `"org_role": "member"`, `"org_role": "admin"`, 1)
	g.commitOnBranch("", member, "roster fix", []file{{path: g.p.PromotionPath, data: []byte(promoted)}})
	g.st.HeldMessages = append(g.st.HeldMessages, "org file fix", "roster fix")
	return nil
}

// extraRefs writes the refs a busy store has besides its branch: the
// signals ref, 15 layer refs and 30 layer-trash refs, each one commit
// with a row on the branch's tip.
func (g *gen) extraRefs() error {
	a := g.p.People[0]
	refs := []string{"refs/lore/signals/" + g.p.Branch}
	for i := range 15 {
		refs = append(refs, fmt.Sprintf("refs/lore/layer/%s/%s/rep_%04d", g.p.Branch, g.p.Folders[0], i))
	}
	for i := range 30 {
		refs = append(refs, fmt.Sprintf("refs/lore/layer-trash/%s/%s/rep_%04d", g.p.Branch, g.p.Folders[0], i))
	}
	for _, ref := range refs {
		fs, err := g.newFiles(1, a)
		if err != nil {
			return err
		}
		g.begin(ref, g.branchTip(), a, "lore: layer "+ref, "")
		g.files(fs)
		g.st.SideCommits++
	}
	g.st.Refs = refs
	return nil
}

// singular is a table's name in a commit message (memories ─▶ memory).
func singular(table string) string {
	switch {
	case strings.HasSuffix(table, "ies"):
		return strings.TrimSuffix(table, "ies") + "y"
	case strings.HasSuffix(table, "es") && strings.HasSuffix(table, "xes"):
		return strings.TrimSuffix(table, "es")
	default:
		return strings.TrimSuffix(table, "s")
	}
}
