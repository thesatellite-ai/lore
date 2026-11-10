package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestClientReadsNoStoreResources (RES 3.9, OV52): an org's own repo lists
// resources of an attached store beside its own; the same checkout opened
// as a client repo reads only the repo store's resources: list, show,
// report and lint never surface the store's rows, even with the store
// attached on this machine.
func TestClientReadsNoStoreResources(t *testing.T) {
	t.Parallel()
	team := newStore(t, teamStoreID)
	own := opstest.Bare(t, ownRepo)
	if err := os.MkdirAll(filepath.Join(own.Repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	yaml := "stores:\n  - name: kb\n    url: " + team + "\n"
	if err := os.WriteFile(filepath.Join(own.Repo, filepath.FromSlash(attach.FileName)), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	own.SeedProject()
	mustAll(t, own,
		[2]string{settings.OpKindAdd, `{"kind":"host","to":"kb"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`},
		[2]string{settings.OpResAdd, `{"kind":"host","name":"agencybox","set":["os=linux"],"to":"kb"}`},
		[2]string{settings.OpResAdd, `{"kind":"host","name":"clientbox","set":["os=bsd"]}`},
	)
	got := opstest.Text(own.Must(settings.OpResList, `{"kind":"host"}`))
	if !strings.Contains(got, "agencybox") || !strings.Contains(got, "clientbox") {
		t.Fatalf("own repo list %q", got)
	}

	client := own.Reopen() // no RepoIsClient hook: fails closed to client
	got = opstest.Text(client.Must(settings.OpResList, `{"kind":"host"}`))
	if strings.Contains(got, "agencybox") || !strings.Contains(got, "clientbox") {
		t.Fatalf("client list %q", got)
	}
	client.Fails(settings.OpResShow, `{"id":"agencybox"}`, errs.NotFound)
	if rep := opstest.Text(client.Must(settings.OpResReport, `{}`)); strings.Contains(rep, "agencybox") || !strings.Contains(rep, "resources   1") {
		t.Errorf("client report %q", rep)
	}
	if lint := client.JSON(client.Must(settings.OpResLint, `{}`)); strings.Contains(lint, "agencybox") {
		t.Errorf("client lint %q", lint)
	}
}
