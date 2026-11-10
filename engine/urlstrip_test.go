package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestStripURLs pins the write-time strip (D24/D25, 14.2): url-named text
// fields and url values lose credentials, a credential warns with the
// clean URL only, a plain user name is silent, other fields are untouched
// and an unreadable value is refused.
func TestStripURLs(t *testing.T) {
	t.Parallel()
	tok := "ghp_" + strings.Repeat("B", 24)
	u, err := value.URLOf("https://mira:" + tok + "@docs.example/x")
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]value.Value{
		"origin_url": txt("https://" + tok + "@github.com/acme/hub.git"),
		"base_url":   txt("https://mira@docs.example/"),
		"link":       u,
		"body":       txt("see https://" + tok + "@github.com/acme/hub.git"),
		"remote":     txt("git@github.com:acme/hub.git"),
	}
	out, warns, err := stripURLs(in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"origin_url": "https://github.com/acme/hub.git", "base_url": "https://docs.example/",
		"link": "https://docs.example/x", "remote": "git@github.com:acme/hub.git",
	}
	for k, w := range want {
		if out[k].String() != w {
			t.Errorf("%s = %q, want %q", k, out[k].String(), w)
		}
	}
	if out["body"].String() != in["body"].String() {
		t.Errorf("a body field is the scanner's job, not the strip's")
	}
	if len(warns) != 2 || warns[0].Field != "link" || warns[1].Field != "origin_url" {
		t.Fatalf("warnings = %+v", warns)
	}
	for _, w := range warns {
		if strings.Contains(w.Message, tok) || w.Code != WarnURLUserinfo {
			t.Fatalf("warning leaks or wrong code: %+v", w)
		}
	}
	if !strings.Contains(in["origin_url"].String(), tok) {
		t.Fatalf("the input map must not be mutated")
	}
	if _, _, err := stripURLs(map[string]value.Value{"origin_url": txt("mira:" + tok + "@github.com/a/b")}); !errs.Has(err, errs.URLUserinfo) {
		t.Fatalf("an unreadable credential must be refused: %v", err)
	} else if strings.Contains(err.Error(), tok) {
		t.Fatalf("the refusal printed the token")
	}
}

// TestClientWriteRefusesSecretRefs pins SQ17/FLAG5 on a write: a row whose
// home is the client repo may hold no ${secret:…} (E_LEAK_BLOCKED), while
// our own store keeps it.
func TestClientWriteRefusesSecretRefs(t *testing.T) {
	t.Parallel()
	w := newF15World(t, true)
	prj := w.project(t, store.SourceRepo, "client")
	_, err := w.eng.Create(context.Background(), store.SourceRepo, schema.TableMemories, map[string]value.Value{
		"project_id": txt(prj.String()), "kind": txt(schema.MemoryKindCore), "body": txt("deploy with ${secret:gopass://acme/x}"), "source_kind": txt("manual"),
	})
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("a secret ref into the client repo: %v", err)
	}
	w.memIn(t, agency, prj, "deploy with ${secret:gopass://acme/x}")
}
