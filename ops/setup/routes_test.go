package setup

// routes_test.go — every setup name stays reachable over REST: no literal
// GET route of the setup ops sits one segment under /v1/setup, where it
// would shadow GET /v1/setup/{id} (setup.show) for a setup of that name.
// The trash list sat at GET /v1/setup/setup, so `lore setup show setup`
// worked and REST answered the trash instead.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/op"
)

func TestSetupRoutesNeverShadowShow(t *testing.T) {
	const base = op.RESTPrefix + "/" + restNoun + "/"
	showPath := base + "{id}"
	routes := map[string]string{}
	for _, e := range GuidedOps(newFakeEnv()) {
		rt, ok := op.RouteOf(e.Spec(), e.InputFields())
		if !ok {
			continue
		}
		routes[e.Spec().Name] = rt.Method + " " + rt.Path
		seg, under := strings.CutPrefix(rt.Path, base)
		if !under || rt.Method != http.MethodGet || rt.Path == showPath || strings.Contains(seg, "/") {
			continue
		}
		if onboard.ValidSetupName(seg) {
			t.Errorf("%s: GET %s shadows setup.show for a setup named %q", e.Spec().Name, rt.Path, seg)
		}
	}
	for opName, want := range map[string]string{
		OpShow:         "GET " + showPath,
		OpTrashList:    "GET /v1/setup/trash/setups",
		OpTrashRestore: "POST /v1/setup/trash/setups/restore",
		OpTrashPurge:   "POST /v1/setup/trash/setups/purge",
	} {
		if routes[opName] != want {
			t.Errorf("%s route = %q, want %q", opName, routes[opName], want)
		}
	}
}
