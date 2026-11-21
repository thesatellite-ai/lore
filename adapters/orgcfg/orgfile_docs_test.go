package orgcfg

// orgfile_docs_test.go — the lore-org.yaml key table in
// docs/reference/config.md is checked against OrgFileDoc, the struct every
// lore reads and writes the file with (devco:D-874, bug hunt H-121). The
// table had fallen behind the code: `stores[].audience` (D-442, D-727)
// and `min_lore_version` (D-735) were missing, and it still offered
// `defaults.client_repos`, which lore migrate moves into the
// routing.client_repos policy (CFG8) and nothing writes any more. A team
// setting up an org from the table could not find the keys their file
// holds. The guard walks the struct's yaml tags, so a key added later
// fails here until the table names it.

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// orgFileDocsPath is the reference page that documents lore-org.yaml.
var orgFileDocsPath = filepath.Join("..", "..", "docs", "reference", "config.md")

// orgFileDocsHeading opens the lore-org.yaml section of that page.
const orgFileDocsHeading = "## `<org_folder>/lore-org.yaml`"

// orgFileLegacyKeys are keys lore still reads from an old file but no
// longer writes; each row must name what replaced it.
var orgFileLegacyKeys = map[string]string{
	"defaults.client_repos": "routing.client_repos",
}

// orgFileKeyPaths lists the dotted yaml paths of t (a struct), parents
// before children: a slice of structs is one path ("stores") whose fields
// follow ("stores.url"); a map is a leaf.
func orgFileKeyPaths(t reflect.Type, prefix string) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		p := prefix + name
		out = append(out, p)
		ft := f.Type
		if ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			out = append(out, orgFileKeyPaths(ft, p+".")...)
		}
	}
	return out
}

// orgFileDocRow is one row of the section's key table: the keys of its
// first cell (normalised: no [] and no .<placeholder>) and its text.
type orgFileDocRow struct {
	keys []string
	text string
}

var (
	docsCode        = regexp.MustCompile("`([^`]+)`")
	docsPlaceholder = regexp.MustCompile(`\.<[^>]+>`)
)

// normDocKey turns a documented key (`stores[].audience`,
// `defaults.client_repos.<kind>`) into a dotted yaml path.
func normDocKey(k string) string {
	return docsPlaceholder.ReplaceAllString(strings.ReplaceAll(k, "[]", ""), "")
}

// orgFileDocRows reads the key table of the lore-org.yaml section.
func orgFileDocRows(t *testing.T) []orgFileDocRow {
	t.Helper()
	b, err := os.ReadFile(orgFileDocsPath)
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(b), orgFileDocsHeading+"\n")
	if !ok {
		t.Fatalf("%s has no %q section", orgFileDocsPath, orgFileDocsHeading)
	}
	if i := strings.Index(sec, "\n## "); i >= 0 {
		sec = sec[:i]
	}
	var rows []orgFileDocRow
	for _, l := range strings.Split(sec, "\n") {
		if !strings.HasPrefix(l, "| `") {
			continue // prose, the header row or its rule
		}
		cells := strings.Split(l, "|")
		r := orgFileDocRow{text: l}
		for _, m := range docsCode.FindAllStringSubmatch(cells[1], -1) {
			r.keys = append(r.keys, normDocKey(m[1]))
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		t.Fatalf("no key table under %q in %s", orgFileDocsHeading, orgFileDocsPath)
	}
	return rows
}

// TestOrgFileKeysDocumented: every key OrgFileDoc reads has a row in the
// reference table (a nested key may instead be named in its parent's row,
// as stores[] names url, name, branch and org_folder), every key the
// table names is one lore reads, and a key lore only reads from old files
// names its replacement.
func TestOrgFileKeysDocumented(t *testing.T) {
	paths := orgFileKeyPaths(reflect.TypeFor[OrgFileDoc](), "")
	rows := orgFileDocRows(t)
	rowOf := func(key string) (orgFileDocRow, bool) {
		for _, r := range rows {
			if slices.Contains(r.keys, key) {
				return r, true
			}
		}
		return orgFileDocRow{}, false
	}
	for _, p := range paths {
		if _, ok := rowOf(p); ok {
			continue
		}
		parent, leaf, nested := strings.Cut(p, ".")
		if nested {
			if r, ok := rowOf(parent); ok && strings.Contains(r.text, "`"+leaf+"`") {
				continue
			}
		}
		if slices.ContainsFunc(paths, func(c string) bool { return strings.HasPrefix(c, p+".") }) {
			continue // a parent documented through its children's rows (defaults)
		}
		t.Errorf("lore-org.yaml key %q has no row in %s (section %s)", p, orgFileDocsPath, orgFileDocsHeading)
	}
	for _, r := range rows {
		for _, k := range r.keys {
			if !slices.Contains(paths, k) {
				t.Errorf("%s documents lore-org.yaml key %q, which lore does not read: %s", orgFileDocsPath, k, r.text)
			}
		}
	}
	for k, repl := range orgFileLegacyKeys {
		r, ok := rowOf(k)
		if !ok {
			continue // reported above
		}
		if !strings.Contains(r.text, "`"+repl) {
			t.Errorf("%s: the %q row must say it moved to %s (lore migrate run moves it; nothing writes it now): %s", orgFileDocsPath, k, repl, r.text)
		}
	}
}
