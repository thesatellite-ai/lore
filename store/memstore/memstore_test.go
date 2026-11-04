package memstore

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/storetest"
)

// fixture is one in-memory location.
type fixture struct{ data *Data }

func (f fixture) Open(_ *testing.T, opts store.OpenOptions) store.Backend { return f.data.Open(opts) }

func TestContract(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(*testing.T) storetest.Fixture { return fixture{data: NewData()} })
}
