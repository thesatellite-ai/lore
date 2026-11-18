package wire

// resourcescope.go — the folder a resource list is scoped to (devco D-583,
// owner use pass 2 Wrong 1). A read of one project folder of an org store
// (`--store <org>/<folder>` from any folder, or run inside that folder of
// the store clone) attaches the org's company folder beside it for its
// kinds, policy and roster; its resources there are the org's, so a list
// keeps the ones stored in the folder and the company folder's whose
// project is the folder (opssettings.ResourceFolderScoper).

import (
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
)

var _ opssettings.ResourceFolderScoper = (*App)(nil)

// ResourceFolderScope implements opssettings.ResourceFolderScoper: the
// folder this open works on when it is one folder of an org store opened
// on its own (a store-only open or a store clone's folder); ok is false in
// a repo and on the store root (--store <org>, the whole org). On the
// company folder itself nothing is attached beside it, so its list keeps
// every row.
func (a *App) ResourceFolderScope() (opssettings.FolderScope, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil || a.st.here == "" {
		return opssettings.FolderScope{}, false
	}
	f := strings.Trim(a.st.folders[a.st.here], "/")
	if f == "" || f == rowfs.FolderRoot {
		return opssettings.FolderScope{}, false
	}
	return opssettings.FolderScope{Source: a.st.here, Folder: f}, true
}
