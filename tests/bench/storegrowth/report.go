package storegrowth

// report.go — one run's numbers as JSON and as a markdown table with the
// same rows every time (the spec's baseline table), so two runs diff by
// eye: `storegrowth-<commits>-<sha>.md` and `.json` in the out folder.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Limit is the longest a measured command may take; a slower one is
// recorded as over it and the run goes on (spec: `>600 s`).
const Limit = 10 * time.Minute

// Sample is one measured command: every run's wall time, or why it has
// none (not run at this size, failed).
type Sample struct {
	Name  string          `json:"name"`
	Runs  []time.Duration `json:"runs,omitempty"`
	Over  bool            `json:"over_limit,omitempty"` // a run hit Limit
	Note  string          `json:"note,omitempty"`       // why there is no time, or what it measured
	Value string          `json:"value,omitempty"`      // a non-time result (sizes, counts)
}

// Median is the middle run (the slower of the two middle ones).
func (s Sample) Median() time.Duration {
	if len(s.Runs) == 0 {
		return 0
	}
	r := slices.Clone(s.Runs)
	slices.Sort(r)
	return r[len(r)/2]
}

// Cell is the sample as one table cell.
func (s Sample) Cell() string {
	switch {
	case s.Over:
		return fmt.Sprintf(">%d s", int(Limit.Seconds()))
	case s.Value != "":
		return s.Value
	case len(s.Runs) == 0:
		return "n/a"
	}
	return FormatDuration(s.Median())
}

// FormatDuration prints ms under a second and s with two decimals above.
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2f s", d.Seconds())
}

// Run is one bench run at one size.
type Run struct {
	Commits   int       `json:"commits"`
	Sha       string    `json:"lore_sha"`
	When      time.Time `json:"when"`
	Generated Stats     `json:"generated"`
	Samples   []Sample  `json:"samples"`
}

// Markdown is the run as a table: metric, median, every run, note.
func (r Run) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Store growth bench: %d commits (lore %s, %s)\n\n", r.Commits, r.Sha, r.When.UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "Generated: %d commits on main (%d merges, %d bulk, %d org file changes), %d side commits, %d edits, %d deletes, rows %v.\n\n",
		r.Generated.Commits, r.Generated.Merges, r.Generated.Bulk, r.Generated.OrgChanges, r.Generated.SideCommits, r.Generated.Edits, r.Generated.Deleted, r.Generated.Rows)
	b.WriteString("| Metric | Median | Runs | Note |\n|---|---|---|---|\n")
	for _, s := range r.Samples {
		runs := make([]string, 0, len(s.Runs))
		for _, d := range s.Runs {
			runs = append(runs, FormatDuration(d))
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", s.Name, s.Cell(), strings.Join(runs, ", "), s.Note)
	}
	return b.String()
}

// Write saves the run as storegrowth-<commits>-<sha>.{md,json} in dir and
// returns the markdown file.
func (r Run) Write(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("storegrowth: report: %w", err)
	}
	base := filepath.Join(dir, fmt.Sprintf("storegrowth-%d-%s", r.Commits, r.Sha))
	js, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("storegrowth: report: %w", err)
	}
	if err := os.WriteFile(base+".json", append(js, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("storegrowth: report: %w", err)
	}
	if err := os.WriteFile(base+".md", []byte(r.Markdown()), 0o644); err != nil {
		return "", fmt.Errorf("storegrowth: report: %w", err)
	}
	return base + ".md", nil
}
