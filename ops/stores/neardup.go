package stores

// neardup.go — how a move into a folder reports the near-duplicates it
// leaves (devco D-724; wire/neardup.go finds them, logic/neardup decides
// what counts). Every id is kept, so a rule the folder already held and
// the moved copy of it both stay; lore lists each pair with the commands
// that compare them and drop one, and never merges.

import (
	"strconv"
	"strings"
)

// NearDuplicate is a moved row that repeats a row the folder already
// held: same table, the same title (case and punctuation aside) or a body
// sharing most of its words. Both are kept.
type NearDuplicate struct {
	// Table and ID are the moved row; Existing the folder's row it repeats.
	Table    string `json:"table"`
	ID       string `json:"id"`
	Existing string `json:"existing"`
	// Title is the moved row's title ("" for a table without titles).
	Title string `json:"title,omitempty"`
	// Why is "same title" or "similar body"; Percent the share of words
	// the bodies have in common (100 for the same title).
	Why     string `json:"why"`
	Percent int    `json:"percent"`
	// Kind is the command noun of the table (rule, memory), "" when the
	// row's own command is not its plain singular.
	Kind string `json:"kind,omitempty"`
}

// nearDupShown is how many pairs the text lists (--json lists every one).
const nearDupShown = 20

// NearDuplicateLines is the text of the pairs a move into place leaves:
// each pair side by side, the command that reads both (named by place, so
// it reads the folder's copy also while the repo still holds the moved
// row) and the one that drops one (to the trash, restorable). dry says
// the move has not run; movedWhen says when the moved row's bare id names
// one row ("" now; a condition such as "once <x>" when a bare id is two
// live rows until then).
func NearDuplicateLines(place string, ds []NearDuplicate, dry bool, movedWhen string) []string {
	return NearDupPrint{Place: place, Dry: dry, MovedWhen: movedWhen}.Lines(ds)
}

// NearDupPrint is how NearDuplicateLines prints for one move: the bulk
// move (`lore move --from`, devco D-724) prints the same pairs as adopt,
// but its dry run reads the moved row where it still is, and a move run
// with --setup prints every command with it, so each runs where printed.
type NearDupPrint struct {
	// Place is the folder or store the rows move into.
	Place string
	// From is where a dry run's moved row is read ("" = Place): before the
	// move it is not in Place yet.
	From string
	// Dry says the move has not run; MovedWhen as NearDuplicateLines.
	Dry       bool
	MovedWhen string
	// Suffix ends every printed lore command (" --setup <name>" or "").
	Suffix string
	// RunIn is the folder a dry run's read and delete of the moved row
	// run in ("" = where the command ran). store adopt --from reads
	// another repo, and From "repo" names the repo of the folder a command
	// runs in, so those commands start with a cd into it (devco D-724):
	// every command a dry run prints works exactly where printed.
	RunIn string
}

// runIn is the "cd <dir> && " a printed command of the moved row starts
// with on a dry run that reads it in another folder ("" otherwise).
func (p NearDupPrint) runIn() string {
	if !p.Dry || p.RunIn == "" {
		return ""
	}
	return "cd " + cdArg(p.RunIn) + " && "
}

// cdArg is dir as one shell word: as is when plain, single-quoted when it
// holds a space or a shell character.
func cdArg(dir string) string {
	if !strings.ContainsAny(dir, " \t'\"$`\\*?&;|<>()#~!{}[]") {
		return dir
	}
	return "'" + strings.ReplaceAll(dir, "'", `'\''`) + "'"
}

// Lines is the text of ds (see NearDuplicateLines).
func (p NearDupPrint) Lines(ds []NearDuplicate) []string {
	if len(ds) == 0 {
		return nil
	}
	place := p.Place
	head := "  " + plural(len(ds), "near-duplicate") + ": moved rows that repeat a row already in " + place + " (both kept; lore never merges, you pick):"
	if p.Dry {
		head = "  would leave " + plural(len(ds), "near-duplicate") + ": rows that repeat a row already in " + place + " (both would be kept; lore never merges):"
	}
	movedAt := place
	if p.Dry && p.From != "" {
		movedAt = p.From
	}
	sfx, cd := p.Suffix, p.runIn()
	lines := []string{head}
	for i, d := range ds {
		if i == nearDupShown {
			lines = append(lines, "    … and "+strconv.Itoa(len(ds)-nearDupShown)+" more (--json lists every pair)")
			break
		}
		why := d.Why
		if d.Percent < 100 {
			why += " " + strconv.Itoa(d.Percent) + "%"
		}
		what := d.Table
		if d.Kind != "" {
			what = d.Kind
		}
		if d.Title != "" {
			what += " " + strconv.Quote(d.Title)
		}
		lines = append(lines, "    "+d.ID+" ~ "+d.Existing+": "+what+" ("+why+")")
		lines = append(lines, "      compare: "+cd+"lore show --id "+d.ID+" --store "+movedAt+sfx+" && lore show --id "+d.Existing+" --store "+place+sfx)
		when := ""
		if p.MovedWhen != "" {
			when = " " + p.MovedWhen
		}
		if d.Kind != "" {
			lines = append(lines, "      drop one: lore "+d.Kind+" delete --id "+d.Existing+" --yes"+sfx+" (the folder's), or the moved one"+when+": "+cd+"lore "+d.Kind+" delete --id "+d.ID+" --yes"+sfx)
		} else {
			lines = append(lines, "      drop one: the delete command `lore show --id "+d.Existing+" --store "+place+sfx+"` names")
		}
	}
	return lines
}
