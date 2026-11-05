// Package trashline is the one human line every noun's delete prints.
// DQ8 made every delete soft (the row goes to the trash), so v0.1.17's
// "deleted (hard)" and the plain "deleted" both stopped telling the truth;
// each delete now says where the row went and how to get it back:
//
//	noun delete → engine.Delete (tombstone + trash) → Moved / MovedBatch
//	                                                     → "✓ … moved to the trash (restore: lore trash restore <id|bat_…>)"
//
// It imports nothing so every ops package (memory included, which sits
// under ops/internal/work) can use it.
package trashline

// Mark is the success mark that starts the line (the same "✓" as
// work.Success and content.Mark).
const Mark = "✓"

// restoreCmd is the command the hint names (DELETION 3.1).
const restoreCmd = "lore trash restore "

// Moved is the line of one deleted row: label is what the user sees
// (usually the id, or "<id> — template X"); id is what restore takes.
func Moved(label, id string) string {
	return Mark + " " + label + " moved to the trash (restore: " + restoreCmd + id + ")\n"
}

// MovedBatch is the line of a delete that took several rows in one batch
// (a task with its sub-tasks, a tasklist with its tasks, a bulk resource
// delete): restoring the batch id brings every row back (DELETION 3.2).
func MovedBatch(what, batch string) string {
	return Mark + " " + what + " moved to the trash (batch " + batch + "; restore: " + restoreCmd + batch + ")\n"
}
