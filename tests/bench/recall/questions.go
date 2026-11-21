package recall

import (
	"fmt"
	"strings"
	"time"
)

// Type is a question type: the six the owner asks every day, then the five
// harder ones.
type Type string

// The question types (owner 2026-11-21).
const (
	TypeWho        Type = "who is X"
	TypeStatus     Type = "project status"
	TypeDecision   Type = "decision and why"
	TypeOwe        Type = "what I owe / who owes me"
	TypeDay        Type = "day D / week W"
	TypeAbout      Type = "everything about X"
	TypeParaphrase Type = "paraphrase"
	TypeTypo       Type = "typo in a name"
	TypeRelative   Type = "relative date"
	TypeNickname   Type = "role or nickname"
	TypeChanged    Type = "fact changed over time"
)

// Types lists every type in report order.
func Types() []Type {
	return []Type{TypeWho, TypeStatus, TypeDecision, TypeOwe, TypeDay, TypeAbout, TypeParaphrase, TypeTypo, TypeRelative, TypeNickname, TypeChanged}
}

// Harder reports whether t is one of the 20 harder questions' types.
func (t Type) Harder() bool {
	switch t {
	case TypeParaphrase, TypeTypo, TypeRelative, TypeNickname, TypeChanged:
		return true
	}
	return false
}

// Typed reports whether t is answered by typed rows (resources, tasks):
// those answers must stay >= 10x lighter than grep's (gate G-4).
func (t Type) Typed() bool { return t == TypeWho || t == TypeOwe }

// Ask is what a question is about, the input of the lore arm's table.
type Ask struct {
	Name   string // a resource name as the question spells it (typos kept)
	Since  string // a date or date phrase
	Until  string
	OwedBy string // task questions: the owed_by / owed_to filter
	OwedTo string
}

// Question is one benchmark question with its expected facts.
type Question struct {
	ID   string
	Type Type
	Text string
	Ask  Ask
	// Required facts: every one must be in the output.
	Required []string
	// Forbidden are stale or wrong facts: one in the output makes the
	// answer wrong, unless Current (the newest value) is in it too, which
	// is how a reader tells the change (see Grade).
	Forbidden []string
	Current   string
	// Lore is the lore arm's one call (arguments after `lore`); Grep is the
	// grep arm's commands, run one by one with bash in the tree's root.
	Lore []string
	Grep []string
}

// loreArm is the lore arm's call per question type: the call an agent
// makes today. Gate G-3 switches a type to `lore ask --query <text>` here,
// in one line.
var loreArm = map[Type]func(q Question) []string{
	TypeWho:      func(q Question) []string { return []string{"resource", "show", "--store", "me", "--id", q.Ask.Name} },
	TypeStatus:   searchFull,
	TypeDecision: searchFull,
	TypeOwe:      taskList,
	TypeDay:      journalRange,
	TypeAbout: func(q Question) []string {
		return []string{"search", "--store", "me", "--query", q.Ask.Name, "--full", "--limit", "50"}
	},
	TypeParaphrase: searchFull,
	TypeTypo:       searchFull,
	TypeRelative:   journalRange,
	TypeNickname:   searchFull,
	TypeChanged:    searchFull,
}

// searchFull asks the question's words of every folder, whole bodies.
func searchFull(q Question) []string {
	return []string{"search", "--store", "me", "--query", q.Text, "--full"}
}

// taskList lists the open tasks owed by or to someone.
func taskList(q Question) []string {
	args := []string{"task", "list", "--store", "me"}
	if q.Ask.OwedBy != "" {
		args = append(args, "--meta", "owed_by="+q.Ask.OwedBy)
	}
	if q.Ask.OwedTo != "" {
		args = append(args, "--meta", "owed_to="+q.Ask.OwedTo)
	}
	return args
}

// journalRange lists the journal entries of a day or a range.
func journalRange(q Question) []string {
	return []string{"memory", "list", "--store", "me/" + FolderJournal, "--since", q.Ask.Since, "--until", q.Ask.Until}
}

// grepCmd builds the grep arm's commands with rg when it is installed,
// else grep with the same meaning.
type grepCmd struct{ rg bool }

// files lists the files under dir holding the fixed text (ignoring case).
func (g grepCmd) files(text, dir string) string {
	if g.rg {
		return fmt.Sprintf("rg -l -i -F -- %s %s", sq(text), dir)
	}
	return fmt.Sprintf("grep -rliF -- %s %s", sq(text), dir)
}

// filesRe is files with a regular expression.
func (g grepCmd) filesRe(re, dir string) string {
	if g.rg {
		return fmt.Sprintf("rg -l -i -- %s %s", sq(re), dir)
	}
	return fmt.Sprintf("grep -rliE -- %s %s", sq(re), dir)
}

// lines prints the lines under path holding the fixed text.
func (g grepCmd) lines(text, path string) string {
	if g.rg {
		return fmt.Sprintf("rg -n -i -F -- %s %s", sq(text), path)
	}
	return fmt.Sprintf("grep -rniF -- %s %s", sq(text), path)
}

// openTasks prints the open task lines of tasks.md holding the text.
func (g grepCmd) openTasks(text string) string {
	if g.rg {
		return fmt.Sprintf("rg -n -F -- '[ ]' tasks.md | rg -i -F -- %s", sq(text))
	}
	return fmt.Sprintf("grep -nF -- '[ ]' tasks.md | grep -iF -- %s", sq(text))
}

// cat prints files.
func cat(paths ...string) string { return "cat " + strings.Join(paths, " ") }

// sq single-quotes s for bash.
func sq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// journalFiles lists the journal files of the days from..to (inclusive).
func journalFiles(from, to time.Time) []string {
	var out []string
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, "journal/"+day(d)+".md")
	}
	return out
}

// eventsBetween lists the journal events of the days from..to.
func (ds *Dataset) eventsBetween(from, to time.Time) []string {
	var out []string
	for _, j := range ds.Journal {
		if !j.Day.Before(from) && !j.Day.After(to) {
			out = append(out, j.Event)
		}
	}
	return out
}

// Questions builds the 50 questions over ds; useRG picks the grep arm's
// tool (rg, or grep where rg is not installed).
func (ds *Dataset) Questions(useRG bool) []Question {
	g := grepCmd{rg: useRG}
	var qs []Question
	addQ := func(q Question) {
		q.ID = fmt.Sprintf("Q%02d", len(qs)+1)
		q.Lore = loreArm[q.Type](q)
		qs = append(qs, q)
	}
	who := func(t Type, text, spelled string, p Person, grep []string) {
		q := Question{Type: t, Text: text, Ask: Ask{Name: spelled}, Required: []string{p.Name, p.Role, p.Company}, Grep: grep}
		if p.Former != "" {
			q.Forbidden, q.Current = []string{p.Former}, p.Company
		}
		addQ(q)
	}
	status := func(t Type, text string, p Project, grep []string) {
		s := ds.status[p.Name]
		addQ(Question{Type: t, Text: text, Required: []string{s[len(s)-1]}, Grep: grep})
	}
	decision := func(t Type, text string, p Project) {
		d := ds.decision[p.Name]
		addQ(Question{Type: t, Text: text, Required: []string{d[0], d[1]}, Grep: []string{g.lines(p.Name, "decisions.md")}})
	}
	owe := func(t Type, text string, by, to, spelledBy, spelledTo string, grep []string) {
		addQ(Question{Type: t, Text: text, Ask: Ask{OwedBy: spelledBy, OwedTo: spelledTo}, Required: ds.tasksOf(by, to, false), Forbidden: ds.tasksOf(by, to, true), Grep: grep})
	}
	journal := func(t Type, text string, from, to time.Time, since, until string) {
		addQ(Question{Type: t, Text: text, Ask: Ask{Since: since, Until: until}, Required: ds.eventsBetween(from, to), Grep: []string{cat(journalFiles(from, to)...)}})
	}

	// 1. who is X (5)
	for _, i := range []int{2, 9, 14, 21, 33} {
		p := ds.People[i]
		who(TypeWho, "who is "+p.Name, p.Name, p, []string{g.files(p.Name, "."), cat("people/" + p.Name + ".md")})
	}
	// 2. project status (5)
	for _, i := range []int{3, 8, 15, 22, 30} {
		p := ds.Projects[i]
		status(TypeStatus, "what is the status of "+p.Name, p, []string{g.files(p.Name, "."), cat("projects/" + p.Name + ".md")})
	}
	// 3. decision and why (5)
	for _, i := range []int{1, 10, 17, 26, 34} {
		p := ds.Projects[i]
		decision(TypeDecision, "what did we decide for "+p.Name+" and why", p)
	}
	// 4. what I owe / who owes me (5)
	for _, i := range []int{3, 6, 18} {
		p := ds.People[i]
		owe(TypeOwe, "what do I owe "+p.Name, Me, p.Name, "", p.Name, []string{g.openTasks("owed to " + p.Name)})
	}
	for _, i := range []int{20, 23} {
		p := ds.People[i]
		owe(TypeOwe, "what does "+p.Name+" owe me", p.Name, Me, p.Name, "", []string{g.openTasks("owed by " + p.Name)})
	}
	// 5. day D / week W (5)
	for _, d := range []int{9, 40, 123} {
		j := ds.journalOn(d)
		journal(TypeDay, "what happened on "+day(j.Day), j.Day, j.Day, day(j.Day), day(j.Day))
	}
	for _, back := range []int{3, 10} {
		mon := monday(ds.Now).AddDate(0, 0, -7*back)
		sun := mon.AddDate(0, 0, 6)
		journal(TypeDay, "what happened in the week of "+day(mon), mon, sun, day(mon), day(sun))
	}
	// 6. everything about X (5)
	for _, i := range []int{4, 11, 18, 25, 37} {
		p := ds.People[i]
		req := []string{p.Role, p.Company}
		if m := ds.meetings[p.Name]; m != "" {
			req = append(req, m)
		}
		if e := ds.newestJournalOf(p.Name); e != "" {
			req = append(req, e)
		}
		if ts := append(ds.tasksOf(Me, p.Name, false), ds.tasksOf(p.Name, Me, false)...); len(ts) > 0 {
			req = append(req, ts[0])
		}
		addQ(Question{Type: TypeAbout, Text: "everything about " + p.Name, Ask: Ask{Name: p.Name}, Required: req, Grep: []string{g.lines(p.Name, "."), cat("people/" + p.Name + ".md")}})
	}

	// harder 1. paraphrase (4): the question's words are not the row's
	falcon, meadow, garnet := ds.Projects[5], ds.Projects[12], ds.Projects[6]
	decision(TypeParaphrase, "what was the reasoning behind "+falcon.Name+"'s pick", falcon)
	status(TypeParaphrase, "how far along is "+meadow.Name+" these days", meadow, []string{g.files(meadow.Name, "."), cat("projects/" + meadow.Name + ".md")})
	b := ds.budgets[garnet.Name]
	addQ(Question{Type: TypeParaphrase, Text: "how much money can " + garnet.Name + " spend now", Required: []string{fmt.Sprintf("%dk", b[1])}, Forbidden: []string{fmt.Sprintf("is %dk", b[0])}, Current: fmt.Sprintf("%dk", b[1]),
		Grep: []string{g.files(garnet.Name, "."), cat("projects/" + garnet.Name + ".md")}})
	cfo := ds.person("hugo-berg")
	addQ(Question{Type: TypeParaphrase, Text: "who handles the money at " + cfo.Company, Required: []string{cfo.Name, cfo.Role},
		Grep: []string{g.files(cfo.Company, "people"), cat(peopleAt(ds, cfo.Company)...)}})

	// harder 2. typo in a name (4)
	alice := ds.person("alice-ng")
	who(TypeTypo, "who is alise-ng", "alise-ng", alice, []string{g.files("alise-ng", "."), g.filesRe("ali.e-ng", "people"), cat("people/alice-ng.md")})
	jonah := ds.person("jonah-park")
	owe(TypeTypo, "what do I owe jonha-park", Me, jonah.Name, "", "jonha-park", []string{g.openTasks("owed to jonha-park"), g.openTasks("park"), g.openTasks("owed to " + jonah.Name)})
	lumen := ds.project("lumen")
	status(TypeTypo, "what is the status of lumne", lumen, []string{g.files("lumne", "."), g.filesRe("lum.?n", "projects"), cat("projects/lumen.md")})
	keiko := ds.person("keiko-sato")
	who(TypeTypo, "who is keiko-satto", "keiko-satto", keiko, []string{g.files("keiko-satto", "."), g.files("keiko", "people"), cat("people/keiko-sato.md")})

	// harder 3. relative dates against the pinned now (4)
	y := ds.journalOn(1).Day
	journal(TypeRelative, "what happened yesterday", y, y, "yesterday", "yesterday")
	y2 := ds.journalOn(2).Day
	journal(TypeRelative, "what happened the day before yesterday", y2, y2, "2 days ago", "2 days ago")
	lmon := monday(ds.Now).AddDate(0, 0, -7)
	journal(TypeRelative, "what happened last week", lmon, lmon.AddDate(0, 0, 6), "last week", "last week")
	fri := fridaysAgo(ds.Now, 2)
	journal(TypeRelative, "what happened two Fridays ago", fri, fri, "2 fridays ago", "2 fridays ago")

	// harder 4. a role or a nickname instead of the name (4)
	bobby := ds.person("robert-kim")
	addQ(Question{Type: TypeNickname, Text: "who is Bobby", Required: []string{bobby.Name, bobby.Role, bobby.Company}, Grep: []string{g.files("bobby", "people"), cat("people/" + bobby.Name + ".md")}})
	fred := ds.person("farid-haddad")
	addQ(Question{Type: TypeNickname, Text: "what do I owe Fred", Required: ds.tasksOf(Me, fred.Name, false), Forbidden: ds.tasksOf(Me, fred.Name, true),
		Grep: []string{g.files("fred", "people"), cat("people/" + fred.Name + ".md"), g.openTasks("owed to " + fred.Name)}})
	addQ(Question{Type: TypeNickname, Text: "who is the CFO", Required: []string{cfo.Name, cfo.Company}, Grep: []string{g.files("cfo", "people"), cat("people/" + cfo.Name + ".md")}})
	lawyer := ds.person("mara-petrov")
	addQ(Question{Type: TypeNickname, Text: "who is our lawyer", Required: []string{lawyer.Name, lawyer.Company}, Grep: []string{g.files("lawyer", "people"), cat("people/" + lawyer.Name + ".md")}})

	// harder 5. a fact that changed: the newest value wins and the answer
	// says it changed (4)
	orbit := ds.Projects[0]
	ob := ds.budgets[orbit.Name]
	changedTo := fmt.Sprintf("moved to %dk (was %dk)", ob[1], ob[0])
	addQ(Question{Type: TypeChanged, Text: "what is the budget for " + orbit.Name + " now", Required: []string{changedTo}, Forbidden: []string{fmt.Sprintf("is %dk", ob[0])}, Current: changedTo,
		Grep: []string{g.files(orbit.Name, "projects"), cat("projects/" + orbit.Name + ".md")}})
	addQ(Question{Type: TypeChanged, Text: "where does alice-ng work now", Required: []string{alice.Company, alice.Former}, Forbidden: []string{"company: " + alice.Former}, Current: alice.Company,
		Grep: []string{g.files(alice.Name, "people"), cat("people/" + alice.Name + ".md")}})
	for _, f := range ds.lifeFacts[:2] {
		now := fmt.Sprintf("changed to %s (was %s)", f[2], f[1])
		addQ(Question{Type: TypeChanged, Text: "what are my " + f[0] + " now", Required: []string{now}, Forbidden: []string{fmt.Sprintf("%s: %s.", f[0], f[1])}, Current: now,
			Grep: []string{g.lines(f[0], "life.md")}})
	}
	return qs
}

// peopleAt lists the people files of a company's people.
func peopleAt(ds *Dataset, company string) []string {
	var out []string
	for _, p := range ds.People {
		if p.Company == company {
			out = append(out, "people/"+p.Name+".md")
		}
	}
	return out
}

// monday is the Monday of t's week.
func monday(t time.Time) time.Time {
	off := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -off)
}

// fridaysAgo is the n-th Friday strictly before t.
func fridaysAgo(t time.Time, n int) time.Time {
	d := t
	for n > 0 {
		d = d.AddDate(0, 0, -1)
		if d.Weekday() == time.Friday {
			n--
		}
	}
	return d
}

// Grade reports whether out answers q: every required fact present, and no
// forbidden (stale or done) fact unless the current value is present too
// (an output that holds the change shows the reader which value is
// current). A partial answer is wrong. missing names what failed.
func Grade(q Question, out string) (correct bool, missing []string) {
	low := strings.ToLower(out)
	has := func(s string) bool { return strings.Contains(low, strings.ToLower(s)) }
	for _, r := range q.Required {
		if !has(r) {
			missing = append(missing, "missing "+r)
		}
	}
	for _, f := range q.Forbidden {
		if has(f) && (q.Current == "" || !has(q.Current)) {
			missing = append(missing, "stale "+f)
		}
	}
	return len(missing) == 0, missing
}
