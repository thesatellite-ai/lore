package doctor

// alltext.go — the text of `lore doctor --all` (ONBOARDING_DESIGN 6.2)
// and of the [setup] section of plain `lore doctor`:
//
//	setup personal (solo, default) ─ ~/www/projects/mira/**
//	  ✓ identity    mira@personal.example
//	  ✓ github      mira-personal via github.com
//	  ✓ store me    read ✓ write ✓ private ✓
//	  ✓ tools       claude_code (skill 0.1.18, hooks ✓)
//	  ✓ secrets     gopass
//	  ⚠ identity.git_email  hub commits as mira@personal.example
//	      fix: git -C /w/hub config user.email mira@agency.example
//
//	2 setups · 31 ok · 1 warning · 0 errors → degraded (exit 1)
//
// Each area (identity, github, each store, tools, secrets) gets one line
// when it has setup-wide results; every warn, fail or could-not-run result
// is then listed by its check id with its fix.

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// Result symbols (2.2: the page shows ✓ ⚠ ✗ – ? for the statuses).
const (
	symOK   = "✓"
	symWarn = "⚠"
	symFail = "✗"
	symSkip = "–"
	// symNote marks an info-level warning: read it, nothing to fix now.
	symNote = "ℹ"
	symErr  = "?"
)

// Area names of the group lines (the check id's prefix before the dot).
const (
	areaIdentity = "identity"
	areaGitHub   = "github"
	areaStore    = "store"
	areaTools    = "tools"
	areaSecrets  = "secrets"
)

// areaWidth is the padded width of an area name (6.2 alignment); a longer
// name gets areaGap spaces instead.
const (
	areaWidth = 12
	areaGap   = 3
)

// defaultGitHubHost is what a setup without a host alias connects to.
const defaultGitHubHost = "github.com"

// symOf is a result's symbol: a warn-level fail is a warning (⚠).
func symOf(r onboard.CheckResult) string {
	if r.Note() {
		return symNote
	}
	switch r.Status {
	case onboard.StatusOK:
		return symOK
	case onboard.StatusWarn:
		return symWarn
	case onboard.StatusFail:
		if r.Level == onboard.LevelError {
			return symFail
		}
		return symWarn
	case onboard.StatusSkip:
		return symSkip
	}
	return symErr
}

// problem reports whether a result is listed on its own line.
func problem(r onboard.CheckResult) bool {
	return r.Status == onboard.StatusWarn || r.Status == onboard.StatusFail || r.Status == onboard.StatusError
}

// repoScoped reports whether a result is about one repo (an absolute root,
// or "<tool> <root>"): such results never colour an area line.
func repoScoped(target string) bool {
	if filepath.IsAbs(target) || strings.HasPrefix(target, "/") {
		return true
	}
	_, rest, ok := strings.Cut(target, " ")
	return ok && strings.HasPrefix(rest, "/")
}

// area is the area of a result: the check id's prefix, a store's name.
func area(r onboard.CheckResult) string {
	pre, _, _ := strings.Cut(string(r.Check), ".")
	if pre == areaStore {
		return r.Target
	}
	return pre
}

// setupHeader is "setup <name> (<kind>[ of <org>][, default]) ─ <folders>".
func setupHeader(s SetupReport) string {
	kind := s.Kind
	if s.Org != "" {
		kind += " of " + s.Org
	}
	if s.Default {
		kind += ", default"
	}
	h := "setup " + s.Name + " (" + kind + ")"
	if len(s.Folders) > 0 {
		h += " ─ " + strings.Join(s.Folders, ", ")
	}
	return h
}

// writeSetupBody writes the area lines and the problems of one setup,
// every line starting with indent.
func writeSetupBody(b *strings.Builder, s SetupReport, indent string) {
	order := []string{}
	byArea := map[string][]onboard.CheckResult{}
	for _, r := range s.Checks {
		a := area(r)
		switch {
		case a == areaIdentity, a == areaGitHub, a == areaTools, a == areaSecrets, strings.HasPrefix(a, targetStore):
		default:
			continue
		}
		if repoScoped(r.Target) && a != areaTools {
			continue
		}
		if _, seen := byArea[a]; !seen {
			order = append(order, a)
		}
		byArea[a] = append(byArea[a], r)
	}
	for _, a := range areaOrder(order) {
		rs := byArea[a]
		sym := symOK
		for _, r := range rs {
			if repoScoped(r.Target) {
				continue
			}
			sym = worseSym(sym, symOf(r))
		}
		fmt.Fprintf(b, "%s\n", strings.TrimRight(indent+sym+" "+padArea(a)+areaLine(a, rs, s), " "))
	}
	broken := false
	for _, r := range s.Checks {
		if !problem(r) {
			continue
		}
		writeProblem(b, r, indent)
		if r.Status == onboard.StatusFail && r.Level == onboard.LevelError {
			broken = true
		}
	}
	if broken {
		fmt.Fprintf(b, "%s    repair: lore setup repair %s\n", indent, s.Name)
	}
}

// writeProblem writes one problem line and its fix line.
func writeProblem(b *strings.Builder, r onboard.CheckResult, indent string) {
	detail := r.Detail
	if l := targetLabel(r.Target); l != "" && !strings.HasPrefix(detail, l) {
		if detail == "" {
			detail = l
		} else {
			detail = l + ": " + detail
		}
	}
	fmt.Fprintf(b, "%s%s %s  %s\n", indent, symOf(r), r.Check, detail)
	if fix := fixLine(r.Fix); fix != "" {
		fmt.Fprintf(b, "%s    fix: %s\n", indent, fix)
	}
}

// fixLine is the command when the fix has one, else its text, followed by
// each choice's command when it offers choices.
func fixLine(f onboard.Fix) string {
	if len(f.Command) > 0 {
		return strings.Join(f.Command, " ")
	}
	cmds := make([]string, 0, len(f.Choices))
	for _, c := range f.Choices {
		cmds = append(cmds, strings.Join(c.Command, " "))
	}
	if len(cmds) > 0 {
		return f.Text + ": " + strings.Join(cmds, " | ")
	}
	return f.Text
}

// targetLabel is a short name for a target: a store's name, a repo's
// folder name, "tool in repo" for repo × tool.
func targetLabel(t string) string {
	switch {
	case t == TargetSetup || t == TargetMachine:
		return ""
	case strings.HasPrefix(t, targetStore):
		return strings.TrimPrefix(t, targetStore)
	case strings.HasPrefix(t, "/"):
		return filepath.Base(t)
	}
	if tool, root, ok := strings.Cut(t, " "); ok && strings.HasPrefix(root, "/") {
		return tool + " in " + filepath.Base(root)
	}
	return t
}

// areaOrder sorts areas as 6.2 prints them: identity, github, stores (in
// first-seen order), tools, secrets.
func areaOrder(seen []string) []string {
	var out, stores []string
	has := map[string]bool{}
	for _, a := range seen {
		has[a] = true
		if strings.HasPrefix(a, targetStore) {
			stores = append(stores, a)
		}
	}
	for _, a := range []string{areaIdentity, areaGitHub} {
		if has[a] {
			out = append(out, a)
		}
	}
	out = append(out, stores...)
	for _, a := range []string{areaTools, areaSecrets} {
		if has[a] {
			out = append(out, a)
		}
	}
	return out
}

// padArea pads an area name to the 6.2 column.
func padArea(a string) string {
	if len(a) >= areaWidth-1 {
		return a + strings.Repeat(" ", areaGap)
	}
	return a + strings.Repeat(" ", areaWidth-len(a))
}

// worseSym is the worse of two symbols (✗ > ⚠ > ? > ✓ / –).
func worseSym(a, b string) string {
	rank := func(s string) int {
		switch s {
		case symFail:
			return 3
		case symWarn:
			return 2
		case symErr:
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// resultOf finds the first result of check id among rs.
func resultOf(rs []onboard.CheckResult, id onboard.CheckID) (onboard.CheckResult, bool) {
	for _, r := range rs {
		if r.Check == id {
			return r, true
		}
	}
	return onboard.CheckResult{}, false
}

// areaLine is the text after an area's name.
func areaLine(a string, rs []onboard.CheckResult, s SetupReport) string {
	switch {
	case a == areaIdentity:
		email := s.summary.email
		if r, ok := resultOf(rs, onboard.CheckIdentityEmailFormat); ok && email == "" && r.Status == onboard.StatusOK {
			// member/admin: the email lives in orgs[<org>]; the probe names it.
			email = r.Detail
		}
		parts := []string{email}
		if r, ok := resultOf(rs, onboard.CheckIdentityRoster); ok {
			parts = append(parts, "roster "+symOf(r))
		}
		if r, ok := resultOf(rs, onboard.CheckIdentityPeriod); ok {
			if r.Status == onboard.StatusOK && r.Detail != "" {
				parts = append(parts, r.Detail)
			} else {
				parts = append(parts, "period "+symOf(r))
			}
		}
		return strings.Join(nonEmpty(parts), " · ")
	case a == areaGitHub:
		if s.summary.https {
			return s.summary.user + " over https (gh login)"
		}
		host := s.summary.alias
		if host == "" {
			host = defaultGitHubHost
		}
		line := s.summary.user + " via " + host
		if r, ok := resultOf(rs, onboard.CheckGitHubSSHKey); ok && r.Status == onboard.StatusOK && r.Detail != "" {
			line += " (" + r.Detail + ")"
		}
		return line
	case strings.HasPrefix(a, targetStore):
		var parts []string
		for _, p := range []struct {
			id   onboard.CheckID
			name string
		}{{onboard.CheckStoreRead, "read"}, {onboard.CheckStoreWrite, "write"}, {onboard.CheckStorePrivate, "private"}, {onboard.CheckStoreMarker, "marker"}} {
			r, ok := resultOf(rs, p.id)
			if !ok || (p.id == onboard.CheckStoreMarker && !problem(r)) {
				continue
			}
			parts = append(parts, p.name+" "+symOf(r))
		}
		if repos := s.summary.storeRepos[a]; len(repos) > 0 {
			// An attached store (#16): which repos attach it, by folder name
			// (each problem line's detail names the full roots).
			names := make([]string, 0, len(repos))
			for _, r := range repos {
				names = append(names, filepath.Base(r))
			}
			parts = append(parts, "· "+repoCount(len(repos))+": "+strings.Join(names, ", "))
		}
		return strings.Join(parts, " ")
	case a == areaTools:
		return toolsLine(rs, s)
	case a == areaSecrets:
		if s.summary.backend == "" {
			return onboard.SecretsNone
		}
		return s.summary.backend
	}
	return ""
}

// repoCount is "1 repo" / "<n> repos".
func repoCount(n int) string {
	if n == 1 {
		return "1 repo"
	}
	return strconv.Itoa(n) + " repos"
}

// toolsLine is "claude_code (skill 0.1.18, hooks ✓), codex (skill 0.1.18)".
func toolsLine(rs []onboard.CheckResult, s SetupReport) string {
	var tools []string
	hooks := map[string]string{}
	for _, r := range rs {
		switch r.Check {
		case onboard.CheckToolsSkillInstalled:
			tools = append(tools, r.Target)
		case onboard.CheckToolsHooks:
			tool, _, _ := strings.Cut(r.Target, " ")
			hooks[tool] = worseSym(hooks[tool], symOf(r))
		}
	}
	var out []string
	for _, t := range tools {
		var in []string
		if v, ok := s.summary.toolVersion[t]; ok {
			if v == "" {
				v = "no version line"
			}
			in = append(in, "skill "+v)
		}
		if h, ok := hooks[t]; ok {
			if h == "" {
				h = symOK
			}
			in = append(in, "hooks "+h)
		}
		if len(in) > 0 {
			t += " (" + strings.Join(in, ", ") + ")"
		}
		out = append(out, t)
	}
	return strings.Join(out, ", ")
}

// nonEmpty drops empty strings.
func nonEmpty(ss []string) []string {
	out := ss[:0:0]
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// plural is "1 warning" / "2 warnings".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// allText is the --all report (6.2): one block per setup, the machine
// problems, this repo's own errors and warnings, then the count line.
func (r Report) allText() string {
	var b strings.Builder
	var ok, warns, errors, couldNot int
	tally := func(rs []onboard.CheckResult) {
		for _, c := range rs {
			switch {
			case c.Status == onboard.StatusOK, c.Note():
				ok++
			case c.Status == onboard.StatusError:
				couldNot++
			case c.Status == onboard.StatusFail && c.Level == onboard.LevelError:
				errors++
			case c.Status == onboard.StatusWarn || c.Status == onboard.StatusFail:
				warns++
			}
		}
	}
	for i, s := range r.Setups {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(setupHeader(s) + "\n")
		writeSetupBody(&b, s, "  ")
		tally(s.Checks)
	}
	for _, n := range r.disabled {
		b.WriteString("\n" + n + "\n")
	}
	for _, n := range r.unchecked {
		b.WriteString("\n" + n + "\n")
	}
	if ps := problems(r.Machine.Checks); len(ps) > 0 {
		b.WriteString("\nmachine\n")
		for _, c := range ps {
			writeProblem(&b, c, "  ")
		}
	}
	tally(r.Machine.Checks)
	if r.repoSkipped > 0 {
		b.WriteString("\nThis repo: " + fmt.Sprintf(noteRepoSkipped, r.repoSkipped) + "\n")
	}
	list(&b, "This repo, errors:", r.Errors)
	list(&b, "This repo, warnings:", r.Warnings)
	// The repo's warnings and errors count like every other result
	// (findings.go): the line never reads "0 warnings" above a degraded verdict.
	tally(r.Findings)
	line := fmt.Sprintf("%s · %d ok · %s · %s", plural(len(r.Setups), "setup"), ok, plural(warns, "warning"), plural(errors, "error"))
	if couldNot > 0 {
		line += fmt.Sprintf(" · %d could not run", couldNot)
	}
	fmt.Fprintf(&b, "\n%s → %s (exit %d)\n", line, r.Status, r.Exit)
	return b.String()
}

// problems filters the results listed on their own line.
func problems(rs []onboard.CheckResult) []onboard.CheckResult {
	var out []onboard.CheckResult
	for _, r := range rs {
		if problem(r) {
			out = append(out, r)
		}
	}
	return out
}

// setupSection is plain doctor's [setup] block for the active setup.
func (r Report) setupSection() string {
	if len(r.Setups) == 0 {
		return ""
	}
	s := r.Setups[0]
	var b strings.Builder
	b.WriteString("[setup]\n")
	why := ""
	if s.Why != "" {
		why = " · chosen by " + s.Why
	}
	fmt.Fprintf(&b, "  %s%s\n", setupHeader(s), why)
	writeSetupBody(&b, s, "  ")
	return b.String()
}
