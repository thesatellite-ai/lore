// Package recall is the "lore beats grep" recall gate (devco D-789, bug
// hunt H-46, gate G-1): one personal store of the shape a person who moved
// their notes into lore ends up with (folders work, life, journal, feeds;
// ~6,000 rows: people, projects and companies as resources, notes,
// journal entries with a day, feed events, tasks owed by or to someone)
// and the same facts as a curated markdown tree. Fifty questions are asked
// of both: the lore arm makes ONE lore call per question, the grep arm runs
// the rg/grep + cat sequence a careful agent would run. Both are graded on
// the same expected facts.
//
// This file holds the parts that need no lore binary (the seeded data set,
// the markdown tree, the questions, grading and the bars), so the plain
// `go test ./tests/bench/recall/` checks them in every gate; the run
// against lore is bench_test.go (build tag bench, task bench:recall).
package recall

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Folder names of the personal store (and the arm they model): the owner's
// shape is notes in work and life, a dated journal and imported feeds.
const (
	FolderWork    = "work"
	FolderLife    = "life"
	FolderJournal = "journal"
	FolderFeeds   = "feeds"
)

// Resource kinds of the store.
const (
	KindPerson  = "person"
	KindProject = "project"
	KindCompany = "company"
)

// Me is the owed_by / owed_to value of the person whose store it is.
const Me = "me"

// DefaultSeed seeds the data set when LORE_BENCH_SEED is unset.
const DefaultSeed = 46

// Person is a person resource.
type Person struct {
	Name     string // dns-shaped resource name, e.g. alice-ng
	Role     string
	Company  string // current company
	Former   string // the company before, when it changed ("" when not)
	MovedOn  time.Time
	Nickname string // what friends call them ("" when none)
	Email    string
}

// Project is a project resource.
type Project struct {
	Name    string
	Owner   string // a person
	Company string
}

// Company is a company resource.
type Company struct {
	Name     string
	Industry string
	City     string
}

// Note is one dated row of the work or life folder (a memory).
type Note struct {
	Folder string
	Day    time.Time
	Body   string
}

// Journal is one journal entry: its day, the event of that day, and the
// person and project it mentions (refs into the work folder).
type Journal struct {
	Day     time.Time
	Event   string
	Person  string
	Project string
}

// Body is the journal row's text; the person and project are written as
// resource references, the way a migrated journal links them.
func (j Journal) Body() string {
	return fmt.Sprintf("Journal %s: %s. Saw ${res:person/%s} about ${res:project/%s}.", day(j.Day), j.Event, j.Person, j.Project)
}

// Markdown is the same entry in the grep arm's journal file.
func (j Journal) Markdown() string {
	return fmt.Sprintf("# %s\n\n%s. Saw [[%s]] about [[%s]].\n", day(j.Day), j.Event, j.Person, j.Project)
}

// Feed is one imported feed event.
type Feed struct {
	Day  time.Time
	Body string
}

// Task is a task owed by one side to the other.
type Task struct {
	Title   string
	OwedBy  string // Me or a person
	OwedTo  string // Me or a person
	Done    bool
	Created time.Time
}

// Dataset is everything the bench writes into lore and into the tree.
type Dataset struct {
	Now       time.Time // the pinned "now" every relative date resolves against
	People    []Person
	Projects  []Project
	Companies []Company
	Notes     []Note
	Journal   []Journal
	Feeds     []Feed
	Tasks     []Task

	// facts the questions are built from
	status    map[string][]string // project -> status phrases, oldest first
	decision  map[string][2]string
	budgets   map[string][2]int // project -> old, new budget (k)
	meetings  map[string]string // person -> newest meeting topic
	lifeFacts [][3]string       // topic, old value, new value
}

// day formats a date the way rows and file names carry it.
func day(t time.Time) string { return t.Format(time.DateOnly) }

var (
	firsts = []string{"alice", "robert", "carmen", "dmitri", "elena", "farid", "grace", "hugo", "ines", "jonah", "keiko", "liam", "mara", "nikhil", "olga", "pavel", "quinn", "rosa", "samir", "tara", "ulrich", "vera", "wen", "ximena", "yusuf", "zoe", "anton", "bianca", "cyrus", "delia", "emil", "fiona", "gideon", "hana", "ivo", "juno", "kofi", "lena", "milo", "nadia"}
	lasts  = []string{"ng", "kim", "ortiz", "volkov", "ruiz", "haddad", "lee", "berg", "costa", "park", "sato", "walsh", "petrov", "rao", "smirnova", "novak", "adams", "diaz", "nasser", "iyer", "brandt", "lopez", "chen", "reyes", "demir", "hart", "kova", "rossi", "farah", "moreau", "lind", "byrne", "stone", "mori", "maric", "ward", "mensah", "fischer", "quill", "aziz"}
	// roles: index i of the people list gets roles[i%len]; the specials
	// (CFO, lawyer) are given once below so a role question has one answer
	roles        = []string{"engineer", "designer", "product manager", "recruiter", "accountant", "investor", "contractor", "analyst", "sales lead", "support lead"}
	companyNames = []string{"acme-co", "birchwood-co", "cobalt-co", "dunmore-co", "elmstead-co", "fairhaven-co"}
	industries   = []string{"logistics", "retail", "fintech", "health", "media", "energy"}
	cities       = []string{"lisbon", "porto", "madrid", "berlin", "dublin", "oslo"}
	projectNames = []string{"orbit", "lumen", "quarry", "harbor", "ember", "falcon", "garnet", "helix", "indigo", "jasper", "kestrel", "lattice", "meadow", "nimbus", "onyx", "prism", "radar", "sable", "tundra", "umber", "vortex", "willow", "xenon", "yarrow", "zephyr", "aspen", "basalt", "cinder", "dune", "fjord", "glacier", "hollow", "iris", "juniper", "krypton", "larch"}
	// status phases: a project walks them in order; the number makes every
	// phrase unique to its project
	phases    = []string{"kickoff held with %d people", "design review passed after %d comments", "beta shipped to %d users", "blocked on the vendor contract for %d days", "load test reached %d requests per second", "live in production with %d customers"}
	choices   = [][2]string{{"postgres", "mongodb"}, {"stripe", "paddle"}, {"hetzner", "aws"}, {"svelte", "react"}, {"weekly releases", "monthly releases"}, {"an in-house team", "an agency"}}
	reasons   = []string{"the reports need joins across %d tables", "fees are %d basis points lower", "the bill is %d percent smaller", "the bundle is %d kb lighter", "customers asked for fixes within %d days", "the agency quoted %d weeks too long"}
	topics    = []string{"hiring plan", "pricing page", "quarterly forecast", "security review", "onboarding flow", "data export", "partner deal", "roadmap", "invoice dispute", "office move"}
	feedSrc   = []string{"github", "slack", "calendar", "mail"}
	feedVerb  = []string{"pushed %d commits to", "posted %d messages about", "scheduled %d meetings on", "sent %d emails about"}
	activity  = []string{"ran along the river", "fixed the kitchen tap", "baked sourdough", "repainted the hallway", "visited the flea market", "read a novel", "cleaned the garage", "hiked the north ridge", "planted tomatoes", "called my sister", "went to the cinema", "repaired the bike", "cooked a curry", "walked the harbour", "sorted old photos", "played chess", "swam at the lido", "learned a song on guitar", "built a bookshelf", "tried a new cafe", "watched the match", "wrote letters", "visited the museum", "drove to the coast", "volunteered at the shelter"}
	actDetail = []string{"in the rain", "before dawn", "with the neighbours", "for two hours", "after lunch", "with a friend", "on a whim", "despite a cold", "in record time", "for the first time", "with music on", "at sunset", "without a plan", "in the snow", "after work", "with the kids", "under a blue sky", "on the balcony", "with old friends", "at a slow pace"}
	owedTitle = []string{"the signed contract", "the q3 forecast", "the design files", "the invoice copy", "the reference letter", "the board deck", "the tax receipt", "the meeting notes", "the price list", "the api keys rotation plan", "the hiring brief"}
)

// Generate builds the data set from seed, with every date relative to now
// (truncated to noon UTC): the shape is fixed by the seed, only the dates
// move with now.
func Generate(seed uint64, now time.Time) *Dataset {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	now = time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	ago := func(d int) time.Time { return now.AddDate(0, 0, -d) }
	ds := &Dataset{Now: now, status: map[string][]string{}, decision: map[string][2]string{}, budgets: map[string][2]int{}, meetings: map[string]string{}}

	for i, c := range companyNames {
		ds.Companies = append(ds.Companies, Company{Name: c, Industry: industries[i], City: cities[i]})
	}
	for i := range firsts {
		p := Person{Name: firsts[i] + "-" + lasts[i], Role: roles[i%len(roles)], Company: companyNames[i%len(companyNames)]}
		p.Email = p.Name + "@people.example"
		ds.People = append(ds.People, p)
	}
	// the specials the role, nickname and changed-fact questions ask about
	ds.People[7].Role = "CFO"         // hugo-berg
	ds.People[12].Role = "lawyer"     // mara-petrov
	ds.People[1].Nickname = "Bobby"   // robert-kim
	ds.People[5].Nickname = "Fred"    // farid-haddad
	ds.People[29].Nickname = "Dee"    // delia-moreau
	ds.People[0].Former = "cobalt-co" // alice-ng moved
	ds.People[0].Company = "acme-co"
	ds.People[0].MovedOn = ago(37)

	for i, n := range projectNames {
		ds.Projects = append(ds.Projects, Project{Name: n, Owner: ds.People[(i*7)%len(ds.People)].Name, Company: companyNames[i%len(companyNames)]})
	}

	// work notes: status updates, one decision per project, budgets that
	// changed, meeting notes
	for i, p := range ds.Projects {
		for k, ph := range phases {
			s := fmt.Sprintf(ph, 10+i*3+k)
			ds.status[p.Name] = append(ds.status[p.Name], s)
			d := 180 - k*30 - (i % 7)
			ds.Notes = append(ds.Notes, Note{Folder: FolderWork, Day: ago(d), Body: fmt.Sprintf("Status update %s (%s): %s.", p.Name, day(ago(d)), s)})
		}
		c := choices[i%len(choices)]
		why := fmt.Sprintf(reasons[i%len(reasons)], 3+i)
		ds.decision[p.Name] = [2]string{c[0], why}
		ds.Notes = append(ds.Notes, Note{Folder: FolderWork, Day: ago(120 + i), Body: fmt.Sprintf("Decision for %s: we chose %s over %s because %s.", p.Name, c[0], c[1], why)})
		if i < 20 {
			old, nw := 40+i*5, 55+i*7
			ds.budgets[p.Name] = [2]int{old, nw}
			ds.Notes = append(ds.Notes,
				Note{Folder: FolderWork, Day: ago(100 + i), Body: fmt.Sprintf("Budget for %s is %dk for this year.", p.Name, old)},
				Note{Folder: FolderWork, Day: ago(10 + i), Body: fmt.Sprintf("Budget for %s moved to %dk (was %dk).", p.Name, nw, old)})
		}
	}
	// meetings: 400 dated notes, each naming a person and a project; the
	// newest per person is remembered for "everything about X"
	type meet struct {
		d     int
		topic string
	}
	newest := map[string]meet{}
	for m := 0; m < 400; m++ {
		p := ds.People[r.IntN(len(ds.People))]
		pr := ds.Projects[r.IntN(len(ds.Projects))]
		d := 2 + r.IntN(360)
		topic := fmt.Sprintf("%s round %d", topics[r.IntN(len(topics))], m)
		ds.Notes = append(ds.Notes, Note{Folder: FolderWork, Day: ago(d), Body: fmt.Sprintf("Met %s about %s (%s): talked about the %s.", p.Name, pr.Name, day(ago(d)), topic)})
		if n, ok := newest[p.Name]; !ok || d < n.d {
			newest[p.Name] = meet{d, topic}
		}
	}
	for k, v := range newest {
		ds.meetings[k] = v.topic
	}
	// life: facts that changed, plus personal notes
	ds.lifeFacts = [][3]string{{"dentist appointments", "tuesdays", "thursdays"}, {"gym", "ironworks gym", "peakfit gym"}, {"car insurance", "harbourline", "northstar mutual"}}
	for i, f := range ds.lifeFacts {
		ds.Notes = append(ds.Notes,
			Note{Folder: FolderLife, Day: ago(200 + i), Body: fmt.Sprintf("My %s: %s.", f[0], f[1])},
			Note{Folder: FolderLife, Day: ago(20 + i), Body: fmt.Sprintf("My %s changed to %s (was %s).", f[0], f[2], f[1])})
	}
	for i := 0; len(ds.Notes) < 700; i++ {
		d := 1 + r.IntN(400)
		ds.Notes = append(ds.Notes, Note{Folder: FolderLife, Day: ago(d), Body: fmt.Sprintf("Personal note %d (%s): remember the %s at home.", i, day(ago(d)), topics[r.IntN(len(topics))])})
	}

	// journal: one entry a day for the last 500 days, every event unique
	for i := 0; i < 500; i++ {
		ev := activity[i%len(activity)] + " " + actDetail[(i/len(activity))%len(actDetail)]
		ds.Journal = append(ds.Journal, Journal{Day: ago(i + 1), Event: ev, Person: ds.People[r.IntN(len(ds.People))].Name, Project: ds.Projects[r.IntN(len(ds.Projects))].Name})
	}

	// feeds: noise that names people and projects
	for i := 0; i < 4800; i++ {
		d := 1 + r.IntN(500)
		ds.Feeds = append(ds.Feeds, Feed{Day: ago(d), Body: fmt.Sprintf("Feed %s %s: %s %s %s.", feedSrc[r.IntN(len(feedSrc))], day(ago(d)), ds.People[r.IntN(len(ds.People))].Name, fmt.Sprintf(feedVerb[r.IntN(len(feedVerb))], 1+r.IntN(9)), ds.Projects[r.IntN(len(ds.Projects))].Name)})
	}

	// tasks: 60 I owe, 50 owed to me; every tenth is done (a done task is
	// never an open debt)
	for i := 0; i < 110; i++ {
		p := ds.People[(i*3)%len(ds.People)].Name
		what := fmt.Sprintf("%s #%d", owedTitle[i%len(owedTitle)], i+1)
		t := Task{Created: ago(5 + i%60), Done: i%10 == 9}
		if i < 60 {
			t.Title, t.OwedBy, t.OwedTo = fmt.Sprintf("send %s to %s", what, p), Me, p
		} else {
			t.Title, t.OwedBy, t.OwedTo = fmt.Sprintf("%s owes me %s", p, what), p, Me
		}
		ds.Tasks = append(ds.Tasks, t)
	}
	return ds
}

// Rows is the number of data rows the set writes to lore (resources,
// notes, journal entries, feed events and tasks).
func (ds *Dataset) Rows() int {
	return len(ds.People) + len(ds.Projects) + len(ds.Companies) + len(ds.Notes) + len(ds.Journal) + len(ds.Feeds) + len(ds.Tasks)
}

// person finds a person by name.
func (ds *Dataset) person(name string) Person {
	for _, p := range ds.People {
		if p.Name == name {
			return p
		}
	}
	panic("recall: no person " + name)
}

// project finds a project by name.
func (ds *Dataset) project(name string) Project {
	for _, p := range ds.Projects {
		if p.Name == name {
			return p
		}
	}
	panic("recall: no project " + name)
}

// tasksOf lists the titles of tasks owed by -> to (open only, or done
// only).
func (ds *Dataset) tasksOf(by, to string, done bool) []string {
	var out []string
	for _, t := range ds.Tasks {
		if t.OwedBy == by && t.OwedTo == to && t.Done == done {
			out = append(out, t.Title)
		}
	}
	return out
}

// newestJournalOf is the newest journal event that mentions name.
func (ds *Dataset) newestJournalOf(name string) string {
	for _, j := range ds.Journal { // newest first
		if j.Person == name || j.Project == name {
			return j.Event
		}
	}
	return ""
}

// journalOn is the event of one day ago (days >= 1).
func (ds *Dataset) journalOn(daysAgo int) Journal { return ds.Journal[daysAgo-1] }

// WriteTree writes the grep arm's curated markdown tree under root:
// people/, companies/, projects/, journal/YYYY-MM-DD.md, decisions.md,
// tasks.md, life.md and feeds/YYYY-MM.md, the files a tidy person keeps.
func (ds *Dataset) WriteTree(root string) error {
	files := map[string]*strings.Builder{}
	add := func(rel, s string) {
		b, ok := files[rel]
		if !ok {
			b = &strings.Builder{}
			files[rel] = b
		}
		b.WriteString(s)
	}
	for _, p := range ds.People {
		s := fmt.Sprintf("# %s\n\n- Role: %s\n- Company: %s\n- Email: %s\n", p.Name, p.Role, p.Company, p.Email)
		if p.Former != "" {
			s = fmt.Sprintf("# %s\n\n- Role: %s\n- Company: %s (moved from %s on %s)\n- Email: %s\n", p.Name, p.Role, p.Company, p.Former, day(p.MovedOn), p.Email)
		}
		if p.Nickname != "" {
			s += "- Nickname: " + p.Nickname + "\n"
		}
		add("people/"+p.Name+".md", s)
	}
	for _, c := range ds.Companies {
		add("companies/"+c.Name+".md", fmt.Sprintf("# %s\n\n- Industry: %s\n- City: %s\n", c.Name, c.Industry, c.City))
	}
	for _, p := range ds.Projects {
		add("projects/"+p.Name+".md", fmt.Sprintf("# %s\n\n- Owner: %s\n- Company: %s\n\n## Log\n\n", p.Name, p.Owner, p.Company))
	}
	notes := slices.Clone(ds.Notes)
	slices.SortStableFunc(notes, func(a, b Note) int { return a.Day.Compare(b.Day) })
	for _, n := range notes {
		switch {
		case n.Folder == FolderLife:
			add("life.md", fmt.Sprintf("- %s %s\n", day(n.Day), n.Body))
		case strings.HasPrefix(n.Body, "Decision for "):
			add("decisions.md", fmt.Sprintf("- %s %s\n", day(n.Day), n.Body))
		case strings.HasPrefix(n.Body, "Met "):
			add("meetings.md", fmt.Sprintf("- %s\n", n.Body))
		default: // status and budget notes live in the project's log
			for _, p := range ds.Projects {
				if strings.Contains(n.Body, " "+p.Name+" ") {
					add("projects/"+p.Name+".md", fmt.Sprintf("- %s %s\n", day(n.Day), n.Body))
					break
				}
			}
		}
	}
	for _, j := range ds.Journal {
		add("journal/"+day(j.Day)+".md", j.Markdown())
	}
	feeds := slices.Clone(ds.Feeds)
	slices.SortStableFunc(feeds, func(a, b Feed) int { return a.Day.Compare(b.Day) })
	for _, f := range feeds {
		add("feeds/"+f.Day.Format("2006-01")+".md", "- "+f.Body+"\n")
	}
	add("tasks.md", "# Tasks\n\n")
	for _, t := range ds.Tasks {
		box := "[ ]"
		if t.Done {
			box = "[x]"
		}
		add("tasks.md", fmt.Sprintf("- %s %s (owed by %s, owed to %s)\n", box, t.Title, t.OwedBy, t.OwedTo))
	}
	for rel, b := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return fmt.Errorf("recall: tree: %w", err)
		}
		if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
			return fmt.Errorf("recall: tree: %w", err)
		}
	}
	return nil
}
