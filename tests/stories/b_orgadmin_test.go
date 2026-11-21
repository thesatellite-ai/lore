//go:build stories && !windows

package stories

// b_orgadmin_test.go — story B, step 9 (devco:D-852, bug hunt H-109): who
// may change the org's own config is the admin role on the org's roster,
// not an admin setup on the machine. Cora starts the org with `lore org
// create` and no guided setup, so her laptop holds no admin setup; she is
// the roster's admin and changes lore-org.yaml. Nia joins with `lore org
// import` as a member and is refused, by name and by the roster.
//
// devco:D-874 (bug hunt H-121): Cora then builds the org file the way a
// team does (a store for admins only, the lore version the org needs, the
// client-repo routing) and every key the file ends up with is one the
// reference table documents; the routing is the routing.client_repos
// policy, never a defaults key in the file.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// bOpsSlug is the org's admins-only store (D-874).
const bOpsSlug = "github.com/acme/lore-ops"

// orgFileTableKeys are the keys the lore-org.yaml table of the reference
// page names in backticks, as dotted paths (stores[].audience →
// stores.audience; .<kind> placeholders dropped).
func orgFileTableKeys(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "config.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(b), "## `<org_folder>/lore-org.yaml`\n")
	if !ok {
		t.Fatal("docs/reference/config.md has no lore-org.yaml section")
	}
	if i := strings.Index(sec, "\n## "); i >= 0 {
		sec = sec[:i]
	}
	code, ph := regexp.MustCompile("`([^`]+)`"), regexp.MustCompile(`\.<[^>]+>`)
	keys := map[string]bool{}
	for _, l := range strings.Split(sec, "\n") {
		if strings.HasPrefix(l, "| `") {
			for _, m := range code.FindAllStringSubmatch(l, -1) {
				keys[ph.ReplaceAllString(strings.ReplaceAll(m[1], "[]", ""), "")] = true
			}
		}
	}
	return keys
}

// yamlKeyPaths lists the dotted key paths of a yaml node; the items of a
// list share their list's path.
func yamlKeyPaths(n *yaml.Node, prefix string, out map[string]bool) {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			yamlKeyPaths(c, prefix, out)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			out[prefix+k] = true
			yamlKeyPaths(n.Content[i+1], prefix+k+".", out)
		}
	}
}

func TestStoryBOrgAdminIsTheRosterRole(t *testing.T) {
	b := newBCompany(t)
	s := b.s
	const store = "acme_team"

	s.Step("D-852/creator-without-a-setup-changes-the-org-file", func() {
		b.person(t, bCTO)
		b.lore(bCTO, "store", "init", "--name", store, "--url", b.store.SSHURL(), "--yes").OK(t)
		b.lore(bCTO, "org", "create", "--org", bOrg, "--store", store, "--own", "github.com/acme/*",
			"--key", "human:"+bCTO.Email, "--name", bCTO.Name, "--handle", "cora", "--yes").OK(t).Says(t, "org role admin")
		b.lore(bCTO, "org", "config", "set", "--org", bOrg, "--key", "own", "--add", "github.com/acme-labs/*", "--yes").OK(t).
			Says(t, "  + github.com/acme-labs/*", "✓ own in").Never(t, "admin setup")
		b.lore(bCTO, "org", "config", "get", "--org", bOrg, "--key", "own").OK(t).Says(t, "github.com/acme-labs/*")
		b.lore(bCTO, "member", "add", "--email", bHire.Email, "--name", bHire.Name, "--handle", "nia", "--yes").OK(t)
		b.lore(bCTO, "sync").OK(t)
	})

	s.Step("D-874/every-key-of-the-org-file-is-documented", func() {
		ops := s.Remote(bOpsSlug)
		b.lore(bCTO, "org", "store", "add", "--org", bOrg, "--url", ops.SSHURL(), "--audience", "admins", "--push", "--yes").OK(t)
		b.lore(bCTO, "org", "config", "set", "--org", bOrg, "--key", "min_lore_version", "--value", "0.1.18", "--push", "--yes").OK(t)
		// client-repo routing is a policy of the org folder, not a key of the file
		b.lore(bCTO, "policy", "set", "--key", "routing.client_repos", "--value", "task="+bOrg+",memory="+bOrg, "--to", store+"/org", "--yes").OK(t)
		b.lore(bCTO, "sync").OK(t)
		b.lore(bCTO, "policy", "show").OK(t).Says(t, "routing.client_repos\ttask="+bOrg+", memory="+bOrg)
		raw, ok := b.store.Read("refs/heads/main", "org/lore-org.yaml")
		if !ok {
			t.Fatal("the org store holds no org/lore-org.yaml")
		}
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("lore-org.yaml: %v\n%s", err, raw)
		}
		got := map[string]bool{}
		yamlKeyPaths(&doc, "", got)
		for _, k := range []string{"stores.audience", "min_lore_version"} {
			if !got[k] {
				t.Errorf("lore-org.yaml lacks %s after the commands that set it:\n%s", k, raw)
			}
		}
		if got["defaults"] {
			t.Errorf("lore-org.yaml holds defaults: routing is the routing.client_repos policy now:\n%s", raw)
		}
		documented := orgFileTableKeys(t)
		for k := range got {
			parent, leaf, nested := strings.Cut(k, ".")
			if !documented[k] && !(nested && documented[parent] && documented[leaf]) {
				t.Errorf("lore-org.yaml key %s is not in the docs/reference/config.md table:\n%s", k, raw)
			}
		}
	})

	s.Step("D-852/a-member-is-refused-by-the-roster", func() {
		s.AdvanceDays(7)
		b.person(t, bHire)
		b.lore(bHire, "org", "import", "--url", b.store.SSHURL(), "--key", "human:"+bHire.Email, "--name", bHire.Name, "--handle", "nia", "--yes").OK(t)
		r := b.lore(bHire, "org", "config", "set", "--org", bOrg, "--key", "own", "--add", "github.com/nia-tools/*", "--yes").
			Refused(t, "only an admin of "+bOrg, "human:"+bHire.Email, "holds no admin role on the roster of "+bOrg).Never(t, "admin setup")
		s.NamedFlags(t, r)
		b.lore(bHire, "org", "config", "get", "--org", bOrg, "--key", "own").OK(t).Never(t, "github.com/nia-tools/*")
	})
}
