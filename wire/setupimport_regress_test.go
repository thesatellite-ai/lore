package wire_test

// setupimport_regress_test.go — a setup the person removed, trashed or
// disabled is never proposed again by the import scan.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// rwScanOut reads setup.import_scan's proposals.
type rwScanOut struct {
	Proposals []struct {
		Setup json.RawMessage `json:"setup"`
	} `json:"proposals"`
	Covered []string `json:"covered"`
	Banner  string   `json:"banner"`
}

// rwEntry is the part of a proposed entry the test compares.
type rwEntry struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	PersonalStore string `json:"personal_store"`
	Identity      struct {
		Key string `json:"key"`
	} `json:"identity"`
}

func (w *rwWorld) scan() (rwScanOut, []rwEntry) {
	w.T.Helper()
	var out rwScanOut
	rwMust(w, "setup.import_scan", map[string]string{}, &out)
	var es []rwEntry
	for _, p := range out.Proposals {
		var e rwEntry
		if err := json.Unmarshal(p.Setup, &e); err != nil {
			w.T.Fatal(err)
		}
		es = append(es, e)
	}
	return out, es
}

// Finding 891b57d9 / polish item 3 (realmac #20-22, NEW-3): after the
// person moved a setup to the trash or disabled it, Home's import banner
// offered to import it again as "personal-2" — from the identity and
// personal store still in config.yaml, which lore itself had written for
// that setup. A removed, trashed or disabled setup's identity or store is
// never proposed again (the way back is enable / trash restore).
func TestRegress_ImportDoesNotReproposeRemovedSetup(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	const legacy = "identity:\n  key: human:person@example.com\n  name: Pat Example\npersonal:\n  store: git@github.com:octo-personal/lore-me.git\n"
	t.Run("imported then", func(t *testing.T) {
		for _, action := range []string{"setup.remove", "setup.disable"} {
			t.Run(action, func(t *testing.T) {
				w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only"})
				w.WriteConfig(legacy)
				_, props := w.scan()
				if len(props) == 0 {
					t.Fatal("the scan proposes nothing for an install with an identity and a personal store")
				}
				var raw []json.RawMessage
				out, _ := w.scan()
				for _, p := range out.Proposals {
					raw = append(raw, p.Setup)
				}
				var imported rwNone
				rwMust(w, "setup.import", map[string][]json.RawMessage{"setups": raw}, &imported)
				name := props[0].Name
				rwMust(w, action, map[string]string{"name": name}, &imported)
				w.Reopen()
				rwAssertNotReproposed(t, w, props[0])
			})
		}
	})
	// The setup the guided steps made has config.yaml's identity but its
	// own store (the Stores step); trashed or disabled, the scan must not
	// offer it back from the identity left at the top of config.yaml.
	for _, state := range []string{"setup.remove", "setup.disable"} {
		t.Run("same identity own store "+state, func(t *testing.T) {
			w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only"})
			w.WriteConfig(legacy + "setups:\n  - name: work\n    kind: solo\n    default: true\n    personal_store: git@github.com:octo-personal/notes.git\n" +
				"    identity: {key: \"human:person@example.com\", name: Pat Example}\n    github: {user: octo-personal}\n")
			var done rwNone
			rwMust(w, state, map[string]string{"name": "work"}, &done)
			w.Reopen()
			rwAssertNotReproposed(t, w, rwEntry{Name: "work", Kind: "solo", PersonalStore: "git@github.com:octo-personal/notes.git",
				Identity: struct {
					Key string `json:"key"`
				}{Key: "human:person@example.com"}})
		})
	}
}

// rwAssertNotReproposed fails when the scan proposes a solo setup with the
// gone setup's identity key or personal store.
func rwAssertNotReproposed(t *testing.T, w *rwWorld, gone rwEntry) {
	t.Helper()
	out, props := w.scan()
	for _, p := range props {
		if p.Kind != "solo" {
			continue
		}
		sameID := gone.Identity.Key != "" && strings.EqualFold(p.Identity.Key, gone.Identity.Key)
		sameStore := gone.PersonalStore != "" && p.PersonalStore == gone.PersonalStore
		if sameID || sameStore || p.Name == gone.Name+"-2" {
			t.Errorf("the scan proposes %+v again after setup %s was taken away (banner %q)", p, gone.Name, out.Banner)
		}
	}
}
