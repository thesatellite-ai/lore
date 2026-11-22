package renderhost

// render_private_fit.go — the hard budget of the private per-store renders
// (devco D-909, CONTEXT_BUDGET 6.1).
//
// Why: the pinned part of a private file (must rules, critical and high
// hotfixes) used to be "never cut", so a company folder with 175 long must
// rules and 61 hotfixes rendered a 146 KB file over a 32000-byte budget,
// and a tool that caps its instruction files loaded only part of it (rules
// silently missing). Now every store file of a repo fits the budget
// together, pinned rows included:
//
//	pins of every store file, ranked (rankPins):
//	  critical hotfixes ─▶ always-on must rules ─▶ high hotfixes ─▶
//	  path-scoped must rules ─▶ other must rules; newest first within
//	fitPins(room):
//	  every pin as a one-line index entry, in rank order, while it fits
//	  (a pin past the room is "not listed": counted, with the commands)
//	  ─▶ upgrade to the full block in rank order while it fits
//	notes fill what is left (fitPrivate)
//	the texts are measured; over the budget ─▶ refit with less room
//
// The full text of every row stays one command away: `lore show --id`,
// and `lore context --for-file <path>` prints every rule that applies to
// a file in full (that command has no cap).

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/schema"
)

// Rank classes of a pinned row (rankPins): what the most files need comes
// first, so a tight budget keeps it in full.
const (
	pinRankCritical = iota // critical hotfix
	pinRankAlways          // must rule, activation always
	pinRankHigh            // high hotfix
	pinRankGlob            // must rule for some paths (activation glob)
	pinRankOther           // must rule, semantic or manual activation
)

// pinIndexCuts are the text lengths of a one-line index entry, tried in
// order until every pinned row is listed (owner, D-909: "title or first
// sentence, ≤ 120 chars"; shorter when 120 would leave rows unlisted).
var pinIndexCuts = [...]int{120, 80, 48}

// Text of a private file's pinned parts.
const (
	privateRulesHead    = "\n## Rules\n\n"
	privateHotfixHead   = "\n## Hotfixes (pinned, recurring warnings)\n\n"
	privatePinIndexHead = "\n## Pinned rows shown as one line (budget)\n\n" +
		"Each applies to every file unless it says (applies: …). Full text: `lore show --id <id>`; every rule that applies to a file, in full: `lore context --for-file <path>`.\n\n"
)

// Forms of a pinned row in the file.
type pinForm int

const (
	pinFormOut  pinForm = iota // not listed (counted in the file's last line)
	pinFormLine                // one-line index entry
	pinFormFull                // the whole <rule>/<hotfix> block
)

// pinRankOf is the rank class of a pinned row.
func pinRankOf(kind, severity, activation string) int {
	switch {
	case kind == privatePinHotfix && severity == renderHotfixSeverityCritical:
		return pinRankCritical
	case kind == privatePinHotfix:
		return pinRankHigh
	case activation == schema.RuleActivationAlways || activation == "":
		return pinRankAlways
	case activation == schema.RuleActivationGlob:
		return pinRankGlob
	}
	return pinRankOther
}

// pinApplies is where a pinned row applies, for its index line: its globs,
// else its activation; "" for a row that applies everywhere (a hotfix, an
// always-on rule), which the index heading covers.
func pinApplies(kind, activation, globs string) string {
	if kind == privatePinHotfix || activation == "" || activation == schema.RuleActivationAlways {
		return ""
	}
	if g := strings.Join(contextfit.SplitGlobs(globs), ", "); g != "" {
		return g
	}
	return activation
}

// pinIndexLines is a pinned row as one index line at each pinIndexCuts
// length: id, severity, the summary, title or first sentence, and where it
// applies when that is not everywhere.
func pinIndexLines(id, severity, summary, title, body, applies string) [len(pinIndexCuts)]string {
	var out [len(pinIndexCuts)]string
	tail := "\n"
	if applies != "" {
		tail = " (applies: " + applies + ")\n"
	}
	for i, cut := range pinIndexCuts {
		out[i] = "- `" + id + "` [" + severity + "] " + contextfit.ShortFormMax(id, summary, title, body, cut) + tail
	}
	return out
}

// rankPins sorts pins by rank class, then newest first, then id (a stable
// order, so every machine writes the same file from the same rows).
func rankPins(pins []privatePin) {
	slices.SortStableFunc(pins, func(a, b privatePin) int {
		switch {
		case a.rank != b.rank:
			return a.rank - b.rank
		case !a.updated.Equal(b.updated):
			if a.updated.After(b.updated) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.id, b.id)
	})
}

// fitPins chooses each ranked pin's form within room bytes and sets its
// line to the index line it gets: the longest index lines that list every
// pin (pinIndexCuts), else the shortest in rank order while they fit; then
// full blocks in rank order while the total stays within room (a full
// block no longer than its line always wins). room < 0 keeps every pin in
// full (no budget).
func fitPins(pins []privatePin, room int) []pinForm {
	forms := make([]pinForm, len(pins))
	if room < 0 {
		for i := range forms {
			forms[i] = pinFormFull
		}
		return forms
	}
	cut := len(pinIndexCuts) - 1
	for c := range pinIndexCuts {
		n := 0
		for _, p := range pins {
			n += len(p.lines[c])
		}
		if n <= room {
			cut = c
			break
		}
	}
	for i := range pins {
		if pins[i].lines[cut] != "" {
			pins[i].line = pins[i].lines[cut]
		}
	}
	used := 0
	for i, p := range pins {
		if used+len(p.line) > room {
			break // every later pin is out too: what is listed never depends on line lengths further down
		}
		used += len(p.line)
		forms[i] = pinFormLine
	}
	for i, p := range pins {
		if forms[i] != pinFormLine {
			continue
		}
		grow := len(p.text) - len(p.line)
		if grow > 0 && used+grow > room {
			continue // stays a line; a later, shorter block may still fit
		}
		used += grow
		forms[i] = pinFormFull
	}
	return forms
}

// pinCounts is how the pinned rows of a render came out.
type pinCounts struct {
	full, line, out int
}

// pinnedLine is the render's words for the pinned rows (owner, D-909: "N
// pinned rules shown in full, M as one-line index"), "" when all are full.
func (c pinCounts) pinnedLine() string {
	if c.line == 0 && c.out == 0 {
		return ""
	}
	s := fmt.Sprintf("%s in full, %d as one-line index", plural.Of(c.full, "pinned rule or hotfix", "pinned rules and hotfixes"), c.line)
	if c.out > 0 {
		s += fmt.Sprintf(", %d not listed", c.out)
	}
	return s + " (full text: `lore context --for-file <path>` / `lore show --id <id>`)"
}

// pinsOutLine is a store file's line for its pinned rows the budget left
// out of the index too.
func pinsOutLine(n, budget int) string {
	return fmt.Sprintf("\n_%s not listed to stay within %s %d bytes: `lore rule list` and `lore hotfix list` list them; `lore context --for-file <path>` prints every rule for a file in full._\n",
		plural.Of(n, "more pinned row", "more pinned rows"), policy.KeyContextBudgetInternal, budget)
}

// privateFixedReserve is the bytes a store file may add besides its head,
// pins and notes: the pinned headings, the not-listed line, the notes
// heading and the left-out notes line, each at its longest (counts of up
// to 6 digits, a budget of up to 9).
func privateFixedReserve() int {
	const most = 999999999
	return len(privateRulesHead) + len(privateHotfixHead) + len(privatePinIndexHead) +
		len(pinsOutLine(most, most)) + len(leftOutLine(most, most)) + len(privateNotesHead) + len(privateEmpty)
}

// pinnedText is the warning's words for the pinned rows after the fit.
func (c pinCounts) pinnedText() string {
	s := fmt.Sprintf("%d shown in full, %d as a one-line index", c.full, c.line)
	if c.out > 0 {
		s += fmt.Sprintf(", %d not listed", c.out)
	}
	return s + " (full text: `lore context --for-file <path>` / `lore show --id <id>`)"
}

// privateBudgetFloor is the least budget the private files get when
// LORE.md takes nearly all of the ceiling: room for their heads and the
// commands that print the rest.
const privateBudgetFloor = 2000

// clampInternalBudget is the budget the private files are fitted to:
// context.budget.internal as set (0 or less: no budget, which the ceiling
// still clamps), at most policy.ContextCeiling less loreBytes, what
// LORE.md takes of it (D-909: one ceiling for everything lore adds to a
// tool's instruction files).
func clampInternalBudget(setting, loreBytes int) int {
	limit := max(policy.ContextCeiling-loreBytes, privateBudgetFloor)
	if setting <= 0 || setting > limit {
		return limit
	}
	return setting
}

// loreMDShare is the size of the repo's LORE.md, which the tools that
// import the private files load in the same session (0 when there is none:
// a client repo or a linked own repo, D-481).
func loreMDShare(root string) int {
	if root == "" {
		return 0
	}
	return max(fileSize(filepath.Join(root, filepath.FromSlash(LoreMDRel))), 0)
}
