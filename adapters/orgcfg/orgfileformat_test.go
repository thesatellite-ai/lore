package orgcfg

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestOrgFileFormatRead: devco:D-873, lore-org.yaml carries a format
// version (lore_org). A file without it (every file written before the
// key existed) reads as format 1, the current one reads as it is, a newer
// one is E_OLD_LORE naming lore upgrade (no key of it is guessed at), and
// a zero, negative or non-number one is E_CONFIG_INVALID.
func TestOrgFileFormatRead(t *testing.T) {
	doc, err := parseOrgFileDoc("name: acme-co\nown:\n    - git.example.test/acme-co/*\n")
	if err != nil || doc.Format != 1 || doc.Name != "acme-co" {
		t.Fatalf("unversioned file: format %d name %q err %v, want format 1", doc.Format, doc.Name, err)
	}
	if doc, err = parseOrgFileDoc("lore_org: 1\nname: acme-co\n"); err != nil || doc.Format != 1 {
		t.Fatalf("lore_org: 1: format %d err %v", doc.Format, err)
	}
	_, err = parseOrgFileDoc("lore_org: 2\nname: acme-co\n")
	if !errs.Has(err, errs.OldLore) || !strings.Contains(err.Error(), "lore upgrade") || !strings.Contains(err.Error(), "lore_org: 2") {
		t.Fatalf("lore_org: 2 read by a format-1 lore: %v, want E_OLD_LORE naming lore upgrade", err)
	}
	for _, bad := range []string{"lore_org: 0\n", "lore_org: -1\n", "lore_org: two\n"} {
		if _, err := parseOrgFileDoc(bad + "name: acme-co\n"); err == nil || errs.Has(err, errs.OldLore) {
			t.Errorf("%q: %v, want a refusal that is not E_OLD_LORE", bad, err)
		}
	}
	if err := CheckOrgFileFormat(nil); err != nil {
		t.Errorf("CheckOrgFileFormat(absent): %v", err)
	}
	newer, zero := OrgFileFormat+1, 0
	if err := CheckOrgFileFormat(&zero); err == nil || errs.Has(err, errs.OldLore) {
		t.Errorf("CheckOrgFileFormat(0): %v, want E_CONFIG_INVALID", err)
	}
	if err := CheckOrgFileFormat(&newer); !errs.Has(err, errs.OldLore) {
		t.Errorf("CheckOrgFileFormat(newer): %v, want E_OLD_LORE", err)
	}
}

// TestOrgFileFormatWrite: devco:D-873, every write through lore stamps
// lore_org as the file's first key (the migration of an unversioned file,
// as a store's layout commit is lore-store.yaml's), keeps every other key
// (one this lore does not know included: a store move re-encoded the
// struct and dropped it) and the comments, and refuses a file of a newer
// format before changing anything.
func TestOrgFileFormatWrite(t *testing.T) {
	old := []byte("# acme-co's org\nname: acme-co\nstores:\n    - url: git@git.example.test:acme-co/notes.git\n      org_folder: org\nown:\n    - git.example.test/acme-co/*\n# a key a newer lore of format 1 added\nwebsite: https://acme-co.example.test\n")
	newer := []byte("lore_org: 9\nname: acme-co\nstores:\n    - url: git@git.example.test:acme-co/notes.git\n")
	edits := map[string]func([]byte) ([]byte, bool, error){
		"org config set": func(raw []byte) ([]byte, bool, error) {
			return setOrgFileList(raw, "repos", []string{"git.example.test/acme-co/client-*"})
		},
		"min_lore_version": func(raw []byte) ([]byte, bool, error) { return raiseOrgFileFloor(raw, "0.1.20") },
		"org store add": func(raw []byte) ([]byte, bool, error) {
			return appendOrgFileStore(raw, "git.example.test/acme-co/more", OrgFileStore{URL: "git@git.example.test:acme-co/more.git"}, false)
		},
		"org store remove": func(raw []byte) ([]byte, bool, error) { return dropOrgFileStore(raw, "git.example.test/acme-co/notes") },
		"store move": func(raw []byte) ([]byte, bool, error) {
			return moveOrgFileURLs(raw, []string{"ssh://git@git.example.test/acme-co/notes"}, "git@git.example.test:acme-co/notes-moved.git")
		},
	}
	for name, edit := range edits {
		out, _, err := edit(old)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(string(out), "# acme-co's org\nlore_org: 1\nname: acme-co\n") {
			t.Errorf("%s: the file does not start with its comment, lore_org: 1 and its name:\n%s", name, out)
		}
		if !strings.Contains(string(out), "# a key a newer lore of format 1 added\nwebsite: https://acme-co.example.test\n") {
			t.Errorf("%s: dropped a key this lore does not know (or its comment):\n%s", name, out)
		}
		if doc, perr := parseOrgFileDoc(string(out)); perr != nil || doc.Format != OrgFileFormat {
			t.Errorf("%s: result format %d err %v", name, doc.Format, perr)
		}
		if out, _, err := edit(newer); !errs.Has(err, errs.OldLore) || out != nil {
			t.Errorf("%s on lore_org: 9: out %q err %v, want E_OLD_LORE and nothing written", name, out, err)
		}
	}
	// the whole-document writer (org create) puts it first too
	body, err := yaml.Marshal(OrgFileDoc{Format: OrgFileFormat, Name: "acme-co"})
	if err != nil || !strings.HasPrefix(string(body), "lore_org: 1\n") {
		t.Fatalf("OrgFileDoc encodes as %q (%v), want lore_org first", body, err)
	}
}
