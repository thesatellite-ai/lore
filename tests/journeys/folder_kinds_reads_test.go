//go:build journeys && !windows

package journeys

// folder_kinds_reads_test.go — devco D-965 (bug hunt H-224): Nia keeps her
// notes in a store of her own (`lore store init`) and writes them into a
// folder of it from her code repo (`--to kb/app`). `lore search` found the
// architecture note there, yet `lore architecturenote list` said "(no
// architecturenotes)" and `architecturenote search` "(no matches)", also
// with --store kb: a row written into a folder takes the folder's project
// (D-718), and the content kinds' reads kept only the repo's project. Now
// every content kind lists and searches the folder's rows, with and
// without --store.

import (
	"strings"
	"testing"
)

func TestJourneyFolderRowsListedByEveryKind(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nia := w.Persona("Nia Okoro", "nia@acme-co.example")
	lap := nia.Laptop()
	kb := w.Remote("github.com/nia-okoro/kb")
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")
	const folder = "kb/app"

	w.Step("day1/notes-into-a-folder-of-her-store", func() {
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "store", "init", "--name", "kb", "--url", kb.SSHURL(), "--yes").OK(t)
		lap.LoreIn(code, "memory", "add", "--to", folder, "--create", "--body", "the app folder", "--yes").OK(t)
		for _, k := range FolderKinds {
			lap.LoreIn(code, k.AddArgs(folder, k.Mark())...).OK(t)
		}
		lap.LoreIn(code, "handoff", "add", "--store", folder, "--for", nia.Name, "--body", "d965handoff", "--yes").OK(t)
	})

	w.Step("day1/every-kind-lists-and-searches-the-folder", func() {
		lap.LoreIn(code, "search", "--query", "d965architecturenote").OK(t).Says(t, "d965architecturenote", "[kb/app]")
		kinds := append(FolderKinds, FolderKind{Kind: "handoff", Search: true})
		for _, k := range kinds {
			mark := k.Mark()
			reads := [][]string{{k.Kind, "list"}, {k.Kind, "list", "--store", "kb"}, {k.Kind, "list", "--store", folder}}
			if k.Search {
				reads = append(reads, []string{k.Kind, "search", "--query", mark}, []string{k.Kind, "search", "--query", mark, "--store", folder})
			}
			for _, rd := range reads {
				if r := lap.LoreIn(code, rd...).OK(t); !strings.Contains(r.Stdout, mark) {
					t.Errorf("[D-965 %s] `lore %s` misses the row written into %s:\n%s", k.Kind, strings.Join(rd, " "), folder, r)
				}
			}
		}
	})
}
