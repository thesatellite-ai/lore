package rowfs

import (
	"net/url"

	"github.com/thesatellite-ai/lore/model/errs"
)

// FolderParam is the url query parameter that picks the data folder a
// Backend opens (owner decision O1): `file:///srv/store?folder=org`,
// `ssh://git@host/acme/lore-company?folder=acme-hub`. Absent means the root
// folder. The engine builds it from the `folder:` key of
// .lore/stores.yaml, so stores.yaml itself keeps a plain url.
const FolderParam = "folder"

// FolderOf returns the clean folder named by u's query and refuses any
// other query parameter (E_STORE_SCHEME): an unknown parameter would be
// silently ignored otherwise, and a url is where tokens hide.
func FolderOf(u *url.URL) (string, error) {
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", errs.New(errs.StoreScheme, "rowfs: the store url query does not parse")
	}
	for k := range q {
		if k != FolderParam {
			return "", errs.New(errs.StoreScheme, "rowfs: the store url has an unknown parameter "+quote(k))
		}
	}
	if vals := q[FolderParam]; len(vals) > 1 {
		return "", errs.New(errs.StoreScheme, "rowfs: the store url names the folder twice")
	}
	return CleanFolder(q.Get(FolderParam))
}
