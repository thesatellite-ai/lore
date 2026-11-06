package task_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestRelateBlocksSameAsAfter (OV15, TASK 8): relate A --blocks B writes
// on B exactly the blocked_by value `task edit B --after A` writes, and
// relate B --blocked-by A the same again; a second relate adds nothing.
func TestRelateBlocksSameAsAfter(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	a := add(h, "upstream", "")
	viaBlocks := add(h, "via blocks", "")
	viaAfter := add(h, "via after", "")
	viaBlockedBy := add(h, "via blocked-by", "")

	h.Must(opstask.OpRelate, `{"id":"`+a+`","blocks":["`+viaBlocks+`"]}`)
	h.Must(opstask.OpEdit, `{"id":"`+viaAfter+`","after":["`+a+`"]}`)
	h.Must(opstask.OpRelate, `{"id":"`+viaBlockedBy+`","blocked_by":["`+a+`"]}`)

	_, rb := get(h, schema.TableTasks, viaBlocks)
	_, ra := get(h, schema.TableTasks, viaAfter)
	_, rbb := get(h, schema.TableTasks, viaBlockedBy)
	want := field(ra, "blocked_by")
	if want == "" {
		t.Fatal("edit --after wrote no blocked_by")
	}
	if got := field(rb, "blocked_by"); got != want {
		t.Errorf("--blocks wrote %q, edit --after %q", got, want)
	}
	if got := field(rbb, "blocked_by"); got != want {
		t.Errorf("--blocked-by wrote %q, edit --after %q", got, want)
	}
	// Re-running the relate keeps one edge.
	h.Must(opstask.OpRelate, `{"id":"`+a+`","blocks":["`+viaBlocks+`"]}`)
	if _, rb = get(h, schema.TableTasks, viaBlocks); field(rb, "blocked_by") != want {
		t.Errorf("second --blocks changed blocked_by to %q", field(rb, "blocked_by"))
	}
}
