package agentdetect

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// designDoc is the design page whose section 4 holds the detector table,
// relative to this package. docsi/ is the private design overlay (a
// gitignored symlink on the machines where the design is edited); a clean
// clone has no copy, see TestDetectorDocDrift.
const designDoc = "../../docsi/AGENT_IDENTITY_DESIGN.md"

// The section 4 table starts after this heading and ends at the next "## ".
const detectorSectionHeading = "## ✅ 4. The detector table"

// docRowSlug takes the slug from a tool row's first cell: "Tool (`slug`)".
// Rows without a backticked slug (the "other tools" and "generic
// convention" rows) are not detectors and are skipped.
var docRowSlug = regexp.MustCompile("^\\|\\s*[^|]*\\(`([a-z0-9_]+)`\\)\\s*\\|")

// docDetector is one tool row of the doc table: its slug and the first word
// of its confidence cell ("Medium (weak only)" -> medium).
type docDetector struct {
	Tool       string
	Confidence Confidence
}

// readDocDetectors parses the section 4 table of the design page.
func readDocDetectors(path string) ([]docDetector, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	var out []docDetector
	in := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // table rows are long
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, detectorSectionHeading):
			in = true
			continue
		case in && strings.HasPrefix(line, "## "):
			return out, nil
		}
		if !in {
			continue
		}
		m := docRowSlug.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		last := strings.Fields(cells[len(cells)-1])
		conf := Confidence("")
		if len(last) > 0 {
			conf = Confidence(strings.ToLower(last[0]))
		}
		out = append(out, docDetector{Tool: m[1], Confidence: conf})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !in {
		return nil, errors.New("heading " + detectorSectionHeading + " not found")
	}
	return out, nil
}

// TestDetectorDocDrift checks the detector table of AGENT_IDENTITY_DESIGN.md
// section 4 against Builtin() (section 5, "Doc drift"): the same set of tool
// slugs, each with the same confidence level. Order is not compared: the Go
// table is in priority order (Qwen Code before its parent Gemini CLI) while
// the doc groups rows by vendor. Without the private docsi/ overlay (a clean
// clone or CI) there is no doc to compare, so the test skips with a message;
// drift can only be introduced where the doc is edited, and there it runs
// (P3-AGENT-1).
func TestDetectorDocDrift(t *testing.T) {
	path, err := filepath.Abs(designDoc)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
		t.Skipf("design page %s not present (docsi/ overlay missing); the doc-drift check runs where the design is edited", path)
	}
	doc, err := readDocDetectors(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(doc) == 0 {
		t.Fatalf("no tool rows parsed from %s section 4; the table format changed", path)
	}
	docConf := map[string]Confidence{}
	for _, d := range doc {
		if _, dup := docConf[d.Tool]; dup {
			t.Errorf("doc: slug %q listed twice", d.Tool)
		}
		docConf[d.Tool] = d.Confidence
	}
	goConf := map[string]Confidence{}
	for _, d := range Builtin() {
		goConf[d.Tool] = d.Confidence
	}
	for _, s := range slices.Sorted(maps.Keys(goConf)) {
		dc, ok := docConf[s]
		switch {
		case !ok:
			t.Errorf("detector %q is in Builtin() but not in the section 4 table of %s", s, path)
		case dc != goConf[s]:
			t.Errorf("detector %q: confidence %q in Builtin(), %q in the doc table", s, goConf[s], dc)
		}
	}
	for _, s := range slices.Sorted(maps.Keys(docConf)) {
		if _, ok := goConf[s]; !ok {
			t.Errorf("doc table row %q has no detector in Builtin()", s)
		}
	}
}

// TestReadDocDetectors pins the parser on a fixed table, so a parser that
// silently matches nothing cannot make the drift test pass vacuously.
func TestReadDocDetectors(t *testing.T) {
	page := strings.Join([]string{
		"# page",
		"## ✅ 3. Before",
		"| Claude Code (`not_this`) | x | High |",
		detectorSectionHeading,
		"| Tool (slug) | Vars | Confidence |",
		"|---|---|---|",
		"| Claude Code (`claude_code`) | `CLAUDECODE=1` | High |",
		"| Zed agent (`zed`) | weak | Medium (weak only) |",
		"| Other tools seen | Kilo `KILO=1` | Low |",
		"| Generic convention | `AI_AGENT=<name>` | High that it exists; values vary |",
		"## 🟡 5. After",
		"| Late (`late`) | x | Low |",
	}, "\n")
	p := filepath.Join(t.TempDir(), "page.md")
	if err := os.WriteFile(p, []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readDocDetectors(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []docDetector{{Tool: "claude_code", Confidence: High}, {Tool: "zed", Confidence: Medium}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
