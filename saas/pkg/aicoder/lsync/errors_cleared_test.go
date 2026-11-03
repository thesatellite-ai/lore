package lsync

import (
	"strings"
	"testing"
)

// secretCheck refuses any document holding the marker "AKIA".
func secretCheck(divert bool) func(*Options) {
	return func(o *Options) {
		if divert {
			o.Divert = divertMemories
		}
		o.CheckSecrets = func(b []byte) string {
			if strings.Contains(string(b), "AKIA") {
				return "aws key"
			}
			return ""
		}
	}
}

// BUG-OPS1: a row refused for a secret and then deleted must not leave its
// refusal in _lore_sync_errors (doctor reported it forever). Covered for
// the git export path and the live divert path.
func TestDeletingARefusedRowClearsItsError(t *testing.T) {
	for _, divert := range []bool{false, true} {
		name := "export"
		if divert {
			name = "live divert"
		}
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.reconcile()
			mem := e.addMemory("token AKIA-SECRET")
			rel := "memories/" + mem + ".json"
			rep := e.reconcile(secretCheck(divert))
			if !hasKind(rep.Errors, ErrKindSecret) {
				t.Fatalf("setup: the secret must be refused: %+v", rep.Errors)
			}
			if n := e.count(`SELECT COUNT(*) FROM _lore_sync_errors WHERE path = ?`, rel); n != 1 {
				t.Fatalf("setup: refusal not recorded (%d)", n)
			}
			if _, err := e.db.Exec(`DELETE FROM memories WHERE id = ?`, mem); err != nil {
				t.Fatal(err)
			}
			rep = e.reconcile(secretCheck(divert))
			if hasKind(rep.Errors, ErrKindSecret) {
				t.Fatalf("a deleted row is not refused again: %+v", rep.Errors)
			}
			if n := e.count(`SELECT COUNT(*) FROM _lore_sync_errors WHERE path = ?`, rel); n != 0 {
				t.Fatal("the refusal of a deleted row must be cleared")
			}
		})
	}
}

// A live row refused for a secret and then edited clean is diverted, and
// its recorded refusal goes away (the export path already did this).
func TestDivertedRowFixedCleanClearsItsError(t *testing.T) {
	e := newEnv(t)
	e.reconcile()
	mem := e.addMemory("token AKIA-SECRET")
	e.reconcile(secretCheck(true))
	if _, err := e.db.Exec(`UPDATE memories SET body = 'clean' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	rep := e.reconcile(secretCheck(true))
	if len(rep.Diverted) != 1 {
		t.Fatalf("the clean row must be diverted: %+v", rep)
	}
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_errors WHERE path = ?`, "memories/"+mem+".json"); n != 0 {
		t.Fatal("the refusal of a fixed row must be cleared")
	}
}

// A row exported clean, then edited to hold a secret (refused, its old file
// stays), then deleted: the delete removes the file and the refusal.
func TestDeletingAnExportedRefusedRowClearsItsError(t *testing.T) {
	e := newEnv(t)
	mem := e.addMemory("clean")
	e.reconcile(secretCheck(false))
	rel := "memories/" + mem + ".json"
	if _, err := e.db.Exec(`UPDATE memories SET body = 'token AKIA-SECRET' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	if rep := e.reconcile(secretCheck(false)); !hasKind(rep.Errors, ErrKindSecret) {
		t.Fatalf("setup: the secret must be refused: %+v", rep.Errors)
	}
	if _, err := e.db.Exec(`DELETE FROM memories WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	rep := e.reconcile(secretCheck(false))
	if len(rep.Removed) != 1 {
		t.Fatalf("the old file must be removed: %+v", rep)
	}
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_errors WHERE path = ?`, rel); n != 0 {
		t.Fatal("the refusal of a deleted row must be cleared")
	}
}
