package trashline

import "testing"

func TestLines(t *testing.T) {
	if got, want := Moved("rul_1", "rul_1"), "✓ rul_1 moved to the trash (restore: lore trash restore --target rul_1)\n"; got != want {
		t.Errorf("Moved = %q, want %q", got, want)
	}
	if got, want := MovedBatch("3 tasks", "bat_9"), "✓ 3 tasks moved to the trash (batch bat_9; restore: lore trash restore --target bat_9)\n"; got != want {
		t.Errorf("MovedBatch = %q, want %q", got, want)
	}
}
