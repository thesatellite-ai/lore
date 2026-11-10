package onboard

// fixcurrent.go — is a saved fix one this binary would still give?
//
// A setup_progress row keeps the CheckResult it was saved with, fix text
// included, but not the probe vars that filled it. When the catalog's
// wording changes (a new lore, or a check whose cause now maps to another
// variant), the step page used to show the old saved text on open until
// the person pressed Re-check (issue 13). The vars are gone, so the text
// cannot be refilled; instead every catalog template of the check is
// turned into a pattern ({placeholder} matches any value, as FillFix and
// TextFix fill it) and the saved fix is kept only when one matches. A fix
// no template produces is replaced by StaleFix: text only, never runnable,
// so an old command is never offered as today's fix. No network, no probe.

import (
	"regexp"
	"strings"
	"sync"
)

// staleFixText is what a saved row shows instead of a fix the current
// catalog no longer produces. The row keeps its checked_at, which the page
// shows next to it ("checked 3 days ago").
const staleFixText = "This fix was saved by an earlier check and this lore words it differently; press Re-check (or run lore setup check) for the current fix"

// StaleFix is the text-only fix that replaces a saved fix the catalog no
// longer produces (FixCurrent false). Command is empty, never nil.
func StaleFix() Fix {
	return Fix{Text: staleFixText, Command: []string{}}
}

// fixPattern is one catalog template compiled for matching.
type fixPattern struct {
	text     *regexp.Regexp
	command  []*regexp.Regexp
	runnable bool
}

var (
	fixPatternsOnce sync.Once
	fixPatterns     map[CheckID][]fixPattern
)

// templatePattern turns a template into an anchored pattern: literal text
// as is, each {placeholder} any run of characters (empty included: a
// value FillFix filled with "" leaves nothing there).
func templatePattern(tmpl string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`(?s)\A`)
	last := 0
	for _, m := range placeholderRe.FindAllStringIndex(tmpl, -1) {
		b.WriteString(regexp.QuoteMeta(tmpl[last:m[0]]))
		b.WriteString(`.*`)
		last = m[1]
	}
	b.WriteString(regexp.QuoteMeta(tmpl[last:]))
	b.WriteString(`\z`)
	return regexp.MustCompile(b.String())
}

// patternsFor is every template (all steps and variants) of check id.
func patternsFor(id CheckID) []fixPattern {
	fixPatternsOnce.Do(func() {
		fixPatterns = map[CheckID][]fixPattern{}
		for k, f := range fixes {
			p := fixPattern{text: templatePattern(f.Text), runnable: f.Runnable}
			for _, a := range f.Command {
				p.command = append(p.command, templatePattern(a))
			}
			fixPatterns[k.check] = append(fixPatterns[k.check], p)
		}
	})
	return fixPatterns[id]
}

// FixCurrent reports whether f is a fix the catalog gives check id today:
// its text matches one of the check's templates, its command (when it has
// one) matches that template's command argument by argument, and it is
// runnable only when that template is. An empty fix (a passing row) is
// current. A check with no catalog template is never current when it
// carries a fix text: a saved row's fix always came from the catalog
// (ops/setup engine.result), so a check that lost its template lost its fix.
func FixCurrent(id CheckID, f Fix) bool {
	if f.Text == "" && len(f.Command) == 0 {
		return true
	}
	for _, p := range patternsFor(id) {
		if p.matches(f) {
			return true
		}
	}
	return false
}

// matches is f against this one template.
func (p fixPattern) matches(f Fix) bool {
	if !p.text.MatchString(f.Text) {
		return false
	}
	if f.Runnable && !p.runnable {
		return false
	}
	if len(f.Command) == 0 {
		return !f.Runnable // TextFix: the text alone, never runnable
	}
	if len(f.Command) != len(p.command) {
		return false
	}
	for i, a := range f.Command {
		if !p.command[i].MatchString(a) {
			return false
		}
	}
	return true
}

// CurrentFixes returns results with every fix the catalog no longer
// produces replaced by StaleFix (status, detail and checked_at kept).
// results itself is not changed.
func CurrentFixes(results []CheckResult) []CheckResult {
	if results == nil {
		return nil // a row with no checks stays as saved (JSON null vs [] unchanged)
	}
	out := make([]CheckResult, len(results))
	for i, r := range results {
		if !FixCurrent(r.Check, r.Fix) {
			r.Fix = StaleFix()
		}
		out[i] = r
	}
	return out
}
