package main

// Typed bench run summary.
//
// Why it exists: the summary used to travel as map[string]any, and its two
// producers disagreed on number types — computeRunSummary put Go ints in it,
// while a summary read back from the database (ent's JSON column) holds
// float64s. printRunSummary asserted float64, so the live path of
// `lore bench run start` printed every count as 0/0 while the stored copy
// printed correctly. One struct, converted to and from the column's map at
// the single storage boundary, makes that class of bug impossible.

import (
	"encoding/json"
	"fmt"
	"sort"

	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"
)

// benchArmStats is one arm's (or one arm+category's) result tally.
type benchArmStats struct {
	// N counts graded results.
	N int `json:"n"`
	// Pass counts results graded pass.
	Pass int `json:"pass"`
	// PassRate is Pass/N (0 when N is 0).
	PassRate float64 `json:"pass_rate"`
}

// benchRunSummary is a run's rollup. The JSON keys are the stored format of
// bench_runs.summary and of `--json` output; renaming one breaks both.
type benchRunSummary struct {
	// Arms maps an arm ("baseline", "with_skill") to its totals.
	Arms map[string]benchArmStats `json:"arms"`
	// ByCategory maps arm → eval category → totals.
	ByCategory map[string]map[string]benchArmStats `json:"by_category"`
	// DeltaPP is with_skill minus baseline pass rate, in percentage points
	// (0 unless both arms ran).
	DeltaPP float64 `json:"delta_pp"`
	// ComputedAt is when the rollup was made (RFC 3339).
	ComputedAt string `json:"computed_at"`
}

// newArmStats tallies n results of which pass passed.
func newArmStats(n, pass int) benchArmStats {
	s := benchArmStats{N: n, Pass: pass}
	if n > 0 {
		s.PassRate = float64(pass) / float64(n)
	}
	return s
}

// toStored converts the summary to the map ent's JSON column stores. The
// conversion goes through JSON so the stored map is exactly what a later
// read will see (numbers as float64), never Go ints.
func (s benchRunSummary) toStored() (map[string]any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode bench summary: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode bench summary: %w", err)
	}
	return out, nil
}

// parseRunSummary reads a stored summary. A run created before it finished
// stores an empty map, which reads as a zero summary; a map that does not
// decode is reported, never silently shown as zeros.
func parseRunSummary(stored map[string]any) (benchRunSummary, error) {
	var s benchRunSummary
	if len(stored) == 0 {
		return s, nil
	}
	b, err := json.Marshal(stored)
	if err != nil {
		return s, fmt.Errorf("encode stored bench summary: %w", err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("stored bench summary has an unexpected shape: %w", err)
	}
	return s, nil
}

// sortedKeys returns a map's keys in order, for stable output.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// storedRunSummary decodes a run's stored summary, naming the run in the
// error so a bad row is easy to find.
func storedRunSummary(run *ent.BenchRun) (benchRunSummary, error) {
	s, err := parseRunSummary(run.Summary)
	if err != nil {
		return s, errcodes.New(errcodes.Internal, "bench run "+run.Code+": read summary").WithCause(err)
	}
	return s, nil
}
