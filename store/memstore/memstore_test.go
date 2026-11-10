package memstore

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/storetest"
)

// fixture is one in-memory location.
type fixture struct{ data *Data }

func (f fixture) Open(_ *testing.T, opts store.OpenOptions) store.Backend { return f.data.Open(opts) }

// journaledFixture opens the location with the audit journal (later 543).
type journaledFixture struct{ data *Data }

func (f journaledFixture) Open(_ *testing.T, opts store.OpenOptions) store.Backend {
	return f.data.OpenJournaled(opts)
}

func TestContract(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(*testing.T) storetest.Fixture { return fixture{data: NewData()} })
}

// TestContractJournaled runs the suite, AuditJournal case included, on
// the journal-capable handle; the plain handle must not claim it.
func TestContractJournaled(t *testing.T) {
	t.Parallel()
	if _, ok := store.Backend(NewData().Open(store.OpenOptions{})).(store.AuditJournal); ok {
		t.Fatal("the plain Backend claims store.AuditJournal")
	}
	storetest.Run(t, func(*testing.T) storetest.Fixture { return journaledFixture{data: NewData()} })
}
