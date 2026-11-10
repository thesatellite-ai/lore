//go:build userscen

package userscen

// Client repo vs own repo for secrets (BATCH 5, engine refuseClient*): a
// secret reference would show the client our vault locator, so a client
// repo refuses --secret-ref in a row field and in metadata with
// E_LEAK_BLOCKED and "nothing written"; the same writes pass in an own
// repo. A plain variable in a client repo is not shareable (clients see
// ${var:<name>}), and the raw secret never reaches the client repo's
// remote whatever the answer.

import (
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestSecretsClientRepoRefusesReferences(t *testing.T) {
	o := newSecOrgLaptop(t)
	key := secAWSKey()
	o.client.MustLore("whoami").Says(t, "client repo")
	o.own.MustLore("whoami").Says(t, "own repo")

	// own repo: a reference may be written
	o.own.MustLore("rule", "add", "--body", "use "+key, "--to", "repo", "--secret-ref", "gopass://northwind/tools/key")
	o.own.MustLore("secrets", "list").Says(t, "gopass://northwind/tools/key")

	// client repo: refused in a field and in metadata, nothing written
	before := len(secDataFiles(t, o.client))
	o.client.Lore("rule", "add", "--body", "use "+key, "--to", "repo", "--secret-ref", "gopass://northwind/acme/key").
		Fails(t, th.ExitGuard, "E_LEAK_BLOCKED", "client repo", "nothing written").Never(t, key)
	task := secID(t, o.client.Lore("decision", "add", "--title", "pay in cents", "--body", "money in minor units", "--to", "repo", "--json"))
	o.client.Lore("meta", "set", task, "vendor_key="+key, "--secret-ref", "gopass://northwind/acme/vendor").
		Fails(t, th.ExitGuard, "E_LEAK_BLOCKED", "cannot live in a client repo", "nothing written").Never(t, key)
	if after := len(secDataFiles(t, o.client)); after != before+1 { // only the decision
		t.Fatalf("refused writes changed the client repo's files: %d -> %d", before, after)
	}
	o.client.MustLore("secrets", "list").Never(t, "gopass://northwind/acme")
	for _, needle := range []string{"gopass://northwind/acme", key} {
		if hit := secDataHolding(t, o.client, needle); len(hit) != 0 {
			t.Fatalf("client repo files hold %q: %v", needle, hit)
		}
	}

	// A variable in the client repo: not shareable by default.
	o.client.MustLore("vars", "set", "api_host", "api.acme.example", "--to", "repo").Says(t, "not shareable: clients see ${var:<name>}")

	o.client.CommitAll("client rows")
	o.client.AdminPush("origin", "main")
	if found := o.cliR.AllObjectsContain(key, "gopass://northwind"); len(found) != 0 {
		t.Fatalf("the client remote holds %v", found)
	}
}
