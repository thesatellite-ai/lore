package doctor

// findings.go — every warning and error of a doctor report as a listed,
// counted finding (owner bug 2026-11-10: the page said "degraded" beside
// "ok 21 warn 0 fail 0", because the report's top-level warnings, such as
// a stale .lore/LORE.md or a gitignored .lore/data, raised the status but
// were in no list the page showed and in no count).
//
//	base warnings / errors ─┐
//	core check findings ────┼─▶ Warnings / Errors (text, unchanged)
//	setup-section warnings ─┘       │ finalize
//	                                ▼
//	              Findings: one CheckResult each (check, status, level,
//	              detail, fix; target "repo <key>") ─▶ Counts
//	                                │
//	                                ▼
//	              Status = the worst of what the report lists
//	              (setups[].checks, machine.checks, findings)
//
// So the status is never worse than the visible findings and never
// better (TestStatusMatchesVisibleFindings). A finding whose fix is one
// plain lore command (lore render, lore sync) carries it as Fix.Command,
// which doctor.fix runs (the page's Run it).

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// Check ids of findings that come from no named core check: a base
// warning (identity, PATH, sync, push modes …) or a base error.
const (
	CheckWarning onboard.CheckID = "doctor.warning"
	CheckError   onboard.CheckID = "doctor.error"
)

// TargetFinding starts a finding's Target: "repo <key>", where key names
// the finding in doctor.fix (FindingKey).
const TargetFinding = "repo "

// findingKeyLen is the hex length of a finding key (8 bytes of SHA-256).
const findingKeyLen = 16

// FindingKey is the stable key of a finding: its check and its line. The
// same problem gives the same key on the next run, so doctor.fix finds it
// again by key and never runs a command the caller sent.
func FindingKey(check onboard.CheckID, line string) string {
	sum := sha256.Sum256([]byte(string(check) + "\x00" + line))
	return hex.EncodeToString(sum[:])[:findingKeyLen]
}

// findingMeta is what a line's source knew beyond the text: its check,
// its fix, and whether it broke the report (an error-level finding).
type findingMeta struct {
	check  onboard.CheckID
	fix    string
	broken bool
}

// note records the source of a warning or error line for finalize.
func (r *Report) note(line string, m findingMeta) {
	if r.meta == nil {
		r.meta = map[string]findingMeta{}
	}
	r.meta[line] = m
}

// fixCommand finds a runnable fix in a fix text or a warning line: one
// plain `lore …` command (in backticks, or the whole fix text), with no
// placeholder (<name>, …) and no shell syntax. Anything else is text only.
var fixCommand = regexp.MustCompile("`(lore(?: [A-Za-z0-9._/=:@-]+)+)`")

// plainArg is one argument of a runnable fix.
var plainArg = regexp.MustCompile(`^[A-Za-z0-9._/=:@-]+$`)

// RunnableFix is the argv of fix when it is one plain lore command; nil
// otherwise. text is the fix text, or the line when there is none.
func RunnableFix(text string) []string {
	t := strings.TrimSpace(text)
	if m := fixCommand.FindStringSubmatch(t); m != nil {
		return strings.Fields(m[1])
	}
	f := strings.Fields(t)
	if len(f) < 2 || f[0] != "lore" {
		return nil
	}
	for _, a := range f[1:] {
		if !plainArg.MatchString(a) {
			return nil
		}
	}
	return f
}

// finalize turns every warning and error into a finding, counts them, and
// sets the status from what the report lists.
func (r *Report) finalize() {
	r.Findings = []onboard.CheckResult{}
	add := func(line string, st onboard.Status, def onboard.CheckID) {
		m, ok := r.meta[line]
		if !ok {
			m = findingMeta{check: def}
		}
		lvl := onboard.LevelWarn
		if m.broken {
			lvl = onboard.LevelError
		}
		fixText := m.fix
		cmd := RunnableFix(fixText)
		if cmd == nil && fixText == "" {
			cmd = RunnableFix(line)
		}
		var fix onboard.Fix
		if fixText != "" || cmd != nil {
			if fixText == "" {
				fixText = strings.Join(cmd, " ")
			}
			if cmd == nil {
				cmd = []string{}
			}
			fix = onboard.Fix{Text: fixText, Command: cmd, Runnable: len(cmd) > 0}
		}
		f := onboard.CheckResult{Check: m.check, Status: st, Level: lvl, Target: TargetFinding + FindingKey(m.check, line), Detail: line, Fix: fix}
		r.Findings = append(r.Findings, f)
		r.Counts.add(f)
	}
	for _, e := range r.Errors {
		add(e, onboard.StatusFail, CheckError)
	}
	for _, w := range r.Warnings {
		add(w, onboard.StatusWarn, CheckWarning)
	}
	r.Status = r.visibleStatus()
}

// visibleStatus is the worst status of everything the report lists.
func (r *Report) visibleStatus() Health {
	st := statusOf(r.Findings)
	for _, s := range r.Setups {
		st = worse(st, statusOf(s.Checks))
	}
	return worse(st, statusOf(r.Machine.Checks))
}
