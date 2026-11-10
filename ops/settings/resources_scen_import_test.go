package settings_test

// Import refusals as a person reads them: the printed message (the error
// envelope's message, which never carries the wrapped cause) must name the
// row AND its own reason for every way a row can be refused, and keep the
// row's hint; the qualified names <kind>/<name> and <place>:<kind>/<name>
// the resource noun's help promises resolve for show and edit.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourcesScenImportRefusalReason is the input-space matrix of row
// refusals × the envelope message (what the CLI and REST print).
func TestResourcesScenImportRefusalReason(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, ownRepo)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text","required":true,"values":"hetzner,aws"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"cores","type":"number"}`},
		[2]string{settings.OpResAdd, `{"kind":"host","name":"taken","set":["provider=aws"]}`},
	)
	dir := t.TempDir()
	for _, tc := range []struct {
		name, body, reason, hint string
	}{
		{"closed set", `{"resources":[{"kind":"host","name":"a1","fields":{"provider":"gcp"}}]}`, `provider "gcp" is not one of hetzner, aws`, "lore resource kind show host"},
		{"wrong type", `{"resources":[{"kind":"host","name":"a1","fields":{"provider":"aws","cores":"many"}}]}`, "cores", "lore resource kind show host"},
		{"unknown field", `{"resources":[{"kind":"host","name":"a1","fields":{"provider":"aws","zzz":"1"}}]}`, "unknown field zzz", ""},
		{"required missing", `{"resources":[{"kind":"host","name":"a1"}]}`, "required field provider is missing", ""},
		{"unknown kind", `{"resources":[{"kind":"nokind","name":"a1"}]}`, `no resource kind "nokind"`, "lore resource kinds"},
		{"taken", `{"resources":[{"kind":"host","name":"taken","fields":{"provider":"aws"}}]}`, "host taken already exists", "--upsert-by name"},
		{"bad name", `{"resources":[{"kind":"host","name":"Bad","fields":{"provider":"aws"}}]}`, "name format", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_")+".json")
			if err := os.WriteFile(p, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			in, _ := json.Marshal(map[string]string{"file": p})
			_, err := h.RunYes(settings.OpResImport, string(in))
			if !errs.Has(err, errs.ResourceBatch) {
				t.Fatalf("want E_RESOURCE_BATCH, got %v", err)
			}
			body := errs.EnvelopeOf(err).Error
			if !strings.Contains(body.Message, tc.reason) || !strings.Contains(body.Message, "nothing written") {
				t.Fatalf("printed message %q must name the reason %q", body.Message, tc.reason)
			}
			if strings.Count(body.Message, "host a1") > 1 {
				t.Fatalf("the row is named once: %q", body.Message)
			}
			if tc.hint != "" && !strings.Contains(body.Hint, tc.hint) {
				t.Fatalf("hint %q must keep %q", body.Hint, tc.hint)
			}
		})
	}
}

// TestResourcesScenQualifiedNames: every form the noun help promises.
func TestResourcesScenQualifiedNames(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, ownRepo)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpKindAdd, `{"kind":"service"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`},
		[2]string{settings.OpResAdd, `{"kind":"host","name":"api"}`},
		[2]string{settings.OpResAdd, `{"kind":"service","name":"api"}`},
	)
	host := h.ID(h.Must(settings.OpResShow, `{"id":"host/api"}`))
	svc := h.ID(h.Must(settings.OpResShow, `{"id":"repo:service/api"}`))
	if host == svc || !strings.HasPrefix(host, "res_") {
		t.Fatalf("host/api %s and repo:service/api %s are two rows", host, svc)
	}
	h.Fails(settings.OpResShow, `{"id":"api"}`, errs.ResourceAmbiguous)
	h.MustYes(settings.OpResEdit, `{"id":"repo:host/api","set":["os=linux"]}`)
	if got := h.JSON(h.Must(settings.OpResShow, `{"id":"`+host+`"}`)); !strings.Contains(got, `"os":"linux"`) {
		t.Fatalf("edit by qualified name wrote the host: %s", got)
	}
	for _, key := range []string{"repo:host/nope", "elsewhere:host/api", "nokind/api", ":host/api", "repo:/api", "host/"} {
		h.Fails(settings.OpResShow, `{"id":"`+key+`"}`, errs.NotFound)
	}
}
