// Package policy resolves the settings rows of every level a write can see
// into the one effective answer (spec 4.3 rules, DECISIONS_LOG 6 OQ24, DOCS
// O1):
//
//   - definitions (variables, and any other named thing) resolve nearest
//     first: Nearest;
//   - policy values resolve to the STRICTER of every level, with a declared
//     strictness per key (Keys): Resolve;
//   - the one owner exception (D2/D9 changed): a secret rule may be turned
//     off at any level, and the result lists every rule that is off, for
//     lore doctor: ResolveRules.
//
// Layer L1: pure; the engine reads the rows and says which level each came
// from.
package policy

import (
	"cmp"
	"slices"

	"github.com/thesatellite-ai/lore/model/ids"
)

// Level is where a settings row lives, as seen from one repo.
type Level string

// Levels, nearest first (O1: repo → linked store folder → the store's org/
// folder → store root → personal store).
const (
	LevelRepo      Level = "repo"
	LevelFolder    Level = "folder"
	LevelOrg       Level = "org"
	LevelStoreRoot Level = "store_root"
	LevelPersonal  Level = "personal"
)

// Levels lists every level, nearest first. Its order IS the precedence of
// Nearest and the tie-break of Resolve.
func Levels() []Level {
	return []Level{LevelRepo, LevelFolder, LevelOrg, LevelStoreRoot, LevelPersonal}
}

// rank is a level's distance (0 = nearest); an unknown level sorts last.
func (l Level) rank() int {
	if i := slices.Index(Levels(), l); i >= 0 {
		return i
	}
	return len(Levels())
}

// Valid reports a known level.
func (l Level) Valid() bool { return slices.Contains(Levels(), l) }

// Leveled is one candidate definition and the level it was found at.
type Leveled[T any] struct {
	Level  Level
	Source ids.AnyID // the row it came from
	Value  T
}

// Nearest picks the candidate of the nearest level (definitions: a
// variable named at the repo beats the same name in the org folder, D22).
// Two candidates at one level keep the first given, so a caller passing
// stores in stores.yaml order gets BATCH 15's order. ok is false for none.
func Nearest[T any](cands []Leveled[T]) (Leveled[T], bool) {
	if len(cands) == 0 {
		return Leveled[T]{}, false
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.Level.rank() < best.Level.rank() {
			best = c
		}
	}
	return best, true
}

// byLevel sorts entries nearest first, stable on input order.
func byLevel[T any](xs []T, level func(T) Level) {
	slices.SortStableFunc(xs, func(a, b T) int { return cmp.Compare(level(a).rank(), level(b).rank()) })
}
