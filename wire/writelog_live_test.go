package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
)

// TestWrittenLinesNameTheLiveRef (C-5): a row the live backend wrote to
// the local live ref is named there, not as "wrote <file>" for a file the
// write left unchanged; a row written to its file keeps the file line.
func TestWrittenLinesNameTheLiveRef(t *testing.T) {
	t.Parallel()
	ctx, log := WithWriteLog(context.Background())
	liveFile := "/w/app/.lore/data/tasks/tsk_live.json"
	fileFile := "/w/app/.lore/data/notes/not_file.json"
	log.add(Written{Store: "repo", File: liveFile, key: "tasks/tsk_live"})
	log.add(Written{Store: "repo", File: fileFile, key: "notes/not_file"})
	NoteLiveWrite(ctx, row.Table("tasks"), "tsk_live", "refs/lore/live")
	lines := WrittenLines(log.Files())
	if len(lines) != 2 {
		t.Fatalf("lines %q", lines)
	}
	want := "lore: wrote tasks/tsk_live on this clone's refs/lore/live (a live row, not pushed yet; " + liveFile + " is unchanged)"
	if lines[0] != want {
		t.Fatalf("live line %q, want %q", lines[0], want)
	}
	if lines[1] != "lore: wrote "+fileFile {
		t.Fatalf("file line %q", lines[1])
	}
	var many []Written
	for _, id := range []string{"a", "b", "c", "d"} {
		many = append(many, Written{Store: "repo", File: "/w/app/.lore/data/tasks/" + id + ".json", Live: "refs/lore/live", key: "tasks/" + id})
	}
	got := WrittenLines(many)
	if len(got) != 1 || !strings.Contains(got[0], "4 live row(s) on this clone's refs/lore/live") || !strings.Contains(got[0], "not pushed yet") {
		t.Fatalf("grouped live lines %q", got)
	}
}
