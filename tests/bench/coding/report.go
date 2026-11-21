package coding

import (
	"fmt"
	"strings"
	"time"
)

// RunInfo is the report's run section.
type RunInfo struct {
	Lore    string
	Rows    int
	Started time.Time
	Render  []string // always-rules missing from the rendered file
}

// Report is the markdown report: the summary, the bars, a table per arm
// and per task.
func Report(info RunInfo, ts []Task, arms []ArmResult) string {
	var b strings.Builder
	b.WriteString("# Coding-agent context gate\n\n")
	fmt.Fprintf(&b, "- lore: %s\n- rows: %d (rules, hotfixes, decisions, patterns, memories); tasks: %d\n- ran: %s (%s)\n\n",
		info.Lore, info.Rows, len(ts), info.Started.UTC().Format(time.RFC3339), time.Since(info.Started).Round(time.Second))
	fmt.Fprintf(&b, "Summary: %s\n\n", Summary(ts, arms))
	misses := AllMisses(info, ts, arms)
	if len(misses) == 0 {
		b.WriteString("PASS: every arm shows every must-rule and critical hotfix, and at least 80% of what it shows applies.\n\n")
	} else {
		b.WriteString("FAIL:\n\n")
		for _, m := range misses {
			b.WriteString("- " + m + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("| arm | recall (required rows) | tasks complete | precision | chars |\n|---|---|---|---|---|\n")
	for _, a := range arms {
		s := a.Total(ts)
		chars := 0
		for _, n := range a.Chars {
			chars += n
		}
		fmt.Fprintf(&b, "| %s | %d/%d | %d/%d | %d/%d (%.0f%%) | %d |\n", a.Name, s.Hit, s.Required, s.Perfect, len(ts), s.Applies, s.Shown, 100*s.Precision(), chars)
	}
	b.WriteString("\n## Per task\n\n| task | file | required | ")
	for _, a := range arms {
		b.WriteString(a.Name + " missing | " + a.Name + " noise | ")
	}
	b.WriteString("\n|---|---|---|")
	for range arms {
		b.WriteString("---|---|")
	}
	b.WriteString("\n")
	for _, t := range ts {
		fmt.Fprintf(&b, "| %s | `%s` | %s | ", t.ID, t.File, strings.Join(t.Required, " "))
		for _, a := range arms {
			g := a.Grades[t.ID]
			fmt.Fprintf(&b, "%s | %s | ", dash(g.Missing), dash(g.Noise))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Summary is the one line the coordinator copies.
func Summary(ts []Task, arms []ArmResult) string {
	parts := make([]string, 0, len(arms))
	for _, a := range arms {
		parts = append(parts, a.Line(ts))
	}
	return strings.Join(parts, "; ")
}

// AllMisses are every missed bar: the arms', and the always-rules the
// rendered file left out.
func AllMisses(info RunInfo, ts []Task, arms []ArmResult) []string {
	var out []string
	if len(info.Render) > 0 {
		out = append(out, "render: the rendered file leaves out the always-on must-rules "+strings.Join(info.Render, ", "))
	}
	for _, a := range arms {
		out = append(out, a.Misses(ts)...)
	}
	return out
}

// dash is the keys joined, or "-".
func dash(keys []string) string {
	if len(keys) == 0 {
		return "-"
	}
	return strings.Join(keys, " ")
}
