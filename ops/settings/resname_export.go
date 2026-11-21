package settings

import (
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/resname"
	"github.com/thesatellite-ai/lore/store"
)

// ResourceNamed reports whether the resources row r held in src is named
// by key: its res_ id, its bare name, <kind>/<name> or
// <place>:<kind>/<name> (RES 3.4). It is the one name rule of
// ops/internal/resname exported for the reference resolver in wire, so a
// ${res:<key>} in a note finds the same resource `lore resource show --id
// <key>` finds (devco D-764). kindName maps a kind id to its name.
func ResourceNamed(src store.Source, r row.Row, key string, kindName func(id string) string) bool {
	return resname.Matches(resname.Of(src, r), key, resname.Forms(key), kindName)
}

// ResourceAliased reports whether key names the resources row r held in
// src through one of its aliases (resname.ByAlias, devco D-817): the
// reference resolver tries it only when no resource is named by key, as
// lore resource show does, so ${res:Bobby} finds the person whose alias
// is Bobby.
func ResourceAliased(src store.Source, r row.Row, key string, kindName func(id string) string) bool {
	return resname.ByAlias(resname.Of(src, r), key, resname.Forms(key), kindName)
}
