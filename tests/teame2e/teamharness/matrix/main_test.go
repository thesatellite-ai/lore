package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// stream builds a test2json stream from (test, action, output) triples.
func stream(lines ...[3]string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(`{"Action":"` + l[1] + `","Test":"` + l[0] + `","Output":` + quote(l[2]) + "}\n")
	}
	return b.String()
}

// quote JSON-encodes s the way test2json does (newlines escaped).
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// The matrix must never report unbuilt work as a pass and never hide a
// failure behind a pending subtest; an ordinary skip is not "pending".
func TestCollect_States(t *testing.T) {
	in := stream(
		[3]string{"TestF01_A", "output", "    x.go:1: PENDING: not built yet: lore land\n"},
		[3]string{"TestF01_A", "skip", ""},
		[3]string{"TestF02_B", "pass", ""},
		[3]string{"TestF03_C", "fail", ""},
		[3]string{"TestSE_D/one", "output", "PENDING: not built yet: lore live restore\n"},
		[3]string{"TestSE_D/one", "skip", ""},
		[3]string{"TestSE_D/two", "skip", ""},
		[3]string{"TestSE_D", "pass", ""},
		[3]string{"TestMix_E/ok", "pass", ""},
		[3]string{"TestMix_E/bad", "fail", ""},
		[3]string{"TestMix_E/later", "output", "PENDING: not built yet: x\n"},
		[3]string{"TestMix_E/later", "skip", ""},
		[3]string{"TestMix_E", "fail", ""},
		[3]string{"TestPlainSkip_F", "skip", ""},
	)
	rep, err := Collect(strings.NewReader("build noise\n" + in))
	if err != nil {
		t.Fatal(err)
	}
	got := rep.Tests
	want := map[string]State{
		"TestF01_A":       Pending,
		"TestF02_B":       Pass,
		"TestF03_C":       Fail,
		"TestSE_D":        Pending,
		"TestMix_E":       Fail,
		"TestPlainSkip_F": Pass,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tests: %+v", len(got), got)
	}
	for name, st := range want {
		if got[name].state != st {
			t.Errorf("%s: got %s want %s", name, got[name].state, st)
		}
	}
	if got["TestF01_A"].pending != "not built yet: lore land" {
		t.Errorf("pending reason: %q", got["TestF01_A"].pending)
	}
}

func TestGroup(t *testing.T) {
	for in, want := range map[string]string{
		"TestF05_LandingCopy":        "F05",
		"TestE01_Race_TwoClones":     "E01",
		"TestRecipe12_Internal":      "Recipe12",
		"TestSideEffect28_Rewritten": "SideEffect28",
		"TestWorkedExample75_Four":   "WorkedExample75",
	} {
		if got := group(in); got != want {
			t.Errorf("group(%q) = %q want %q", in, got, want)
		}
	}
}

// buildFailStream is what go 1.27 `go test -json` printed for a test package
// that does not compile (captured from a real run, timestamps dropped).
const buildFailStream = `{"ImportPath":"saas/cmd/cli [saas/cmd/cli.test]","Action":"build-output","Output":"# saas/cmd/cli [saas/cmd/cli.test]\n"}
{"ImportPath":"saas/cmd/cli [saas/cmd/cli.test]","Action":"build-output","Output":"./team_e2e_fixtures_test.go:157:6: row redeclared in this block\n"}
{"ImportPath":"saas/cmd/cli [saas/cmd/cli.test]","Action":"build-fail"}
{"Action":"start","Package":"saas/cmd/cli"}
{"Action":"output","Package":"saas/cmd/cli","Output":"FAIL\tsaas/cmd/cli [build failed]\n","OutputType":"frame"}
{"Action":"fail","Package":"saas/cmd/cli","Elapsed":0,"FailedBuild":"saas/cmd/cli [saas/cmd/cli.test]"}
`

// A run must never look green or empty-but-fine: a suite that does not
// compile, a package that fails outside any test, and a run in which no
// test matched are all red, and each says why; only real passes and
// pending work exit 0.
func TestPrint_RunLevelOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantCode int
		wantOut  []string
		notOut   []string
	}{
		{
			name:     "build failure says BUILD FAILED with the compiler line",
			in:       buildFailStream,
			wantCode: 1,
			wantOut:  []string{"BUILD FAILED", "row redeclared in this block"},
			notOut:   []string{"0 pass", "PACKAGE FAILED"},
		},
		{
			name:     "build-fail event alone is enough",
			in:       `{"ImportPath":"p [p.test]","Action":"build-fail"}` + "\n",
			wantCode: 1,
			wantOut:  []string{"BUILD FAILED"},
		},
		{
			name:     "FailedBuild on the package event alone is enough",
			in:       `{"Action":"fail","Package":"p","FailedBuild":"p [p.test]"}` + "\n",
			wantCode: 1,
			wantOut:  []string{"BUILD FAILED"},
		},
		{
			name: "package fails outside any test",
			in: `{"Action":"pass","Package":"p","Test":"TestF01_A"}
{"Action":"output","Package":"p","Output":"panic: boom in TestMain\n"}
{"Action":"fail","Package":"p"}
`,
			wantCode: 1,
			wantOut:  []string{"TestF01_A", "PACKAGE FAILED outside any test: p", "panic: boom in TestMain"},
		},
		{
			name: "a failing test is reported as the test, not as a package failure",
			in: `{"Action":"fail","Package":"p","Test":"TestF01_A"}
{"Action":"fail","Package":"p"}
`,
			wantCode: 1,
			wantOut:  []string{"1 FAIL"},
			notOut:   []string{"PACKAGE FAILED"},
		},
		{
			name:     "no test ran",
			in:       `{"Action":"output","Package":"p","Output":"testing: warning: no tests to run\n"}` + "\n" + `{"Action":"pass","Package":"p"}` + "\n",
			wantCode: 1,
			wantOut:  []string{"NO TESTS RAN"},
		},
		{
			name: "pass and pending only is green",
			in: `{"Action":"pass","Package":"p","Test":"TestF01_A"}
{"Action":"output","Package":"p","Test":"TestF02_B","Output":"PENDING: not built yet: lore land\n"}
{"Action":"skip","Package":"p","Test":"TestF02_B"}
{"Action":"pass","Package":"p"}
`,
			wantCode: 0,
			wantOut:  []string{"1 pass · 0 FAIL · 1 pending · 2 total"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep, err := Collect(strings.NewReader(c.in))
			if err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			if code := Print(&out, rep); code != c.wantCode {
				t.Errorf("exit %d want %d\n%s", code, c.wantCode, out.String())
			}
			for _, w := range c.wantOut {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
			for _, n := range c.notOut {
				if strings.Contains(out.String(), n) {
					t.Errorf("output must not contain %q:\n%s", n, out.String())
				}
			}
		})
	}
}

// A FAIL row carries the first assertion line of the test (or of its failing
// subtest), so a flaky failure in a long run leaves its reason on screen.
func TestPrint_FailRowShowsTheAssertion(t *testing.T) {
	in := stream(
		[3]string{"TestF19_Race/a", "output", "    team_e2e_org_test.go:42: both processes must see one file\n"},
		[3]string{"TestF19_Race/a", "fail", ""},
		[3]string{"TestF19_Race", "fail", ""},
		[3]string{"TestF01_Pend", "output", "    x_test.go:9: PENDING: not built yet: lore land\n"},
		[3]string{"TestF01_Pend", "skip", ""},
	)
	rep, err := Collect(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	Print(&out, rep)
	if !strings.Contains(out.String(), "team_e2e_org_test.go:42: both processes must see one file") {
		t.Fatalf("FAIL row must show the assertion:\n%s", out.String())
	}
	if strings.Count(out.String(), "x_test.go:9") != 0 {
		t.Fatalf("a PENDING line is not a failure line:\n%s", out.String())
	}
}
