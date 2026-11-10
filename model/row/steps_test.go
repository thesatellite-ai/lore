package row

import (
	"strings"
	"testing"
)

// TestStepsWellFormed: steps[i].To == i+2, FormatVersion == len(steps)+1,
// every step has Up and a feature, every Additive step has Needs.
func TestStepsWellFormed(t *testing.T) {
	if FormatVersion != len(steps)+1 {
		t.Fatalf("FormatVersion %d, %d steps", FormatVersion, len(steps))
	}
	for i, s := range steps {
		if s.To != i+2 || s.Up == nil || s.Feature == "" || (s.Kind != Additive && s.Kind != Breaking) {
			t.Fatalf("step %d malformed: %+v", i, s)
		}
		if s.Kind == Additive && s.Needs == nil {
			t.Fatalf("additive step %d has no Needs", i)
		}
	}
}

// TestReleaseHasNoTestSteps: no step of the release registry is a test
// step (the loretest_format step exists only under its build tag).
func TestReleaseHasNoTestSteps(t *testing.T) {
	for _, s := range releaseSteps {
		if strings.HasPrefix(s.Feature, "loretest") {
			t.Fatalf("release step %d is a test step", s.To)
		}
	}
}

// TestWriteVersionLowest: a written row is at the newest Breaking step's
// version unless it uses a later Additive step's shape; a v1 document is
// upcast through every step and re-encodes at that version.
func TestWriteVersionLowest(t *testing.T) {
	r, err := Decode([]byte(`{"_v":1,"_table":"memories","id":"mem_01936f0000007a1b8c2d3e4f5a6b7c8d","updated_at":"2026-11-04T10:42:07.123Z","body":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.HLC.IsZero() || r.SourceVersion != 1 {
		t.Fatalf("upcast: %+v", r)
	}
	if got := writeVersion(r); got != hlcVersion {
		t.Fatalf("writeVersion = %d, want %d (a row that needs no additive step)", got, hlcVersion)
	}
	b, err := Encode(r)
	if err != nil || !strings.HasPrefix(string(b), "{\n  \"_hlc\"") || !strings.Contains(string(b), `"_v": 2`) {
		t.Fatalf("encode: %s %v", b, err)
	}
	if fs := StepFeatures(Table(tablePlans), hlcVersion); len(fs) != 2 || fs[1] != featurePlanStatus {
		t.Fatalf("StepFeatures(plans) = %v", fs)
	}
}
