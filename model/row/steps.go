package row

import (
	"slices"
	"strconv"
)

// The format step registry (FORMAT_EVOLUTION rule 3). Each format version
// after 1 is one Step, in order (steps[i].To == i+2). Readers upgrade every
// document in memory through the steps after its _v (Decode → upcast);
// writers write the lowest version that can hold the row (writeVersion),
// so a row nobody touched is never rewritten.
//
//	Decode(_v n) ──upcast: steps[n-1:]…Up──► Row at FormatVersion
//	Encode(Row)  ──writeVersion: newest Breaking, raised by Additive Needs──► _v
//
// Append only; never edit a released step. A step never reads the clock,
// the database or the environment.

// StepKind classifies a step (rule 5).
type StepKind int

const (
	// Additive means an old document is already valid at To; only documents
	// whose Needs is true are written at To.
	Additive StepKind = iota + 1
	// Breaking means every document written must be at least To.
	Breaking
)

// Step is one format version after 1.
type Step struct {
	// To is the version this step produces.
	To int
	// Tables lists the tables whose documents change; nil means every
	// table.
	Tables []Table
	// Kind is Additive or Breaking.
	Kind StepKind
	// Feature is the formatgate feature a document at version To needs
	// (a name in formatgate.Known; checked by TestStepsWellFormed there).
	Feature string
	// TableFeatures are extra features the step brings for one table (the
	// hlc step's plans part: plan_status, TSQ9).
	TableFeatures map[Table]string
	// Up converts a document of version To-1 into version To. Pure; it
	// returns its input unchanged when there is nothing to do.
	Up func(r Row) (Row, error)
	// Needs reports whether a row uses this step's new shape (Additive
	// steps only).
	Needs func(r Row) bool
}

// AppliesTo reports whether the step changes table's documents.
func (s Step) AppliesTo(t Table) bool { return s.Tables == nil || slices.Contains(s.Tables, t) }

// Feature names the release steps use (formatgate's constants; model may
// not import logic, so formatgate's test pins them equal).
const (
	featureHLC        = "hlc"
	featurePlanStatus = "plan_status"
	tablePlans        = "plans"
)

// hlcVersion is the version that requires _hlc on every row.
const hlcVersion = 2

// releaseSteps is the whole released format history.
var releaseSteps = []Step{
	// 0.1.18 (STORE_SERVER_SPEC 4.1, step list #1 and #6): _hlc on every
	// row, built from updated_at, counter 0000 and updated_by. The plans
	// status_str → status mapping of TSQ9 runs where the closed set lives
	// (wire's migrate rewrite and ops/plan's read), because model cannot
	// see the schema; the step declares its feature here.
	{To: hlcVersion, Kind: Breaking, Feature: featureHLC, TableFeatures: map[Table]string{tablePlans: featurePlanStatus}, Up: v1ToV2},
}

// steps is the registry this build uses: the release steps plus, only in a
// `loretest_format` build, the test step (steps_testformat.go).
var steps = append(slices.Clone(releaseSteps), testSteps...)

// Steps returns a copy of the registry, in order.
func Steps() []Step { return slices.Clone(steps) }

// ReleaseSteps returns a copy of the released steps (no test step).
func ReleaseSteps() []Step { return slices.Clone(releaseSteps) }

// v1ToV2 gives a v1 row its _hlc (spec 12, with the fallbacks of
// upcastHLC).
func v1ToV2(r Row) (Row, error) {
	if r.HLC.IsZero() {
		r.HLC = upcastHLC(r)
	}
	return r, nil
}

// upcast applies every step after from, in order.
func upcast(r Row, from int) (Row, error) {
	for _, s := range steps {
		if s.To <= from || !s.AppliesTo(r.Table) || s.Up == nil {
			continue
		}
		next, err := s.Up(r)
		if err != nil {
			return Row{}, bad("upgrade " + r.Table.String() + " to _v " + strconv.Itoa(s.To) + ": " + err.Error())
		}
		r = next
	}
	return r, nil
}

// writeVersion is the lowest version that can hold r: the newest Breaking
// step, raised to the newest Additive step whose shape r uses. With only
// the 0.1.18 Breaking step every written row is _v 2.
func writeVersion(r Row) int {
	v := formatV1
	for _, s := range steps {
		if !s.AppliesTo(r.Table) {
			continue
		}
		if s.Kind == Breaking || (s.Needs != nil && s.Needs(r)) {
			v = s.To
		}
	}
	return v
}

// WriteVersion is writeVersion for callers that report it (migrate status).
func WriteVersion(r Row) int { return writeVersion(r) }

// StepFeatures lists the features of the steps a row written at version v
// of table uses (the step's Feature plus its table feature).
func StepFeatures(t Table, v int) []string {
	var out []string
	for _, s := range steps {
		if s.To > v || !s.AppliesTo(t) {
			continue
		}
		out = append(out, s.Feature)
		if f, ok := s.TableFeatures[t]; ok {
			out = append(out, f)
		}
	}
	return out
}
