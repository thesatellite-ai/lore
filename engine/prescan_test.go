package engine

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// callScanner counts its scans.
type callScanner struct {
	scan.Scanner
	calls *atomic.Int64
}

func (c callScanner) Scan(field, text string) []scan.Finding {
	c.calls.Add(1)
	return c.Scanner.Scan(field, text)
}

// TestPrescanFillsTheScanMemo (B1-23): Prescan runs the scans a create of
// each row would run, so the creates under the same memo scan nothing
// again; without a memo it does nothing, and a create scans as before.
func TestPrescanFillsTheScanMemo(t *testing.T) {
	t.Parallel()
	inner, err := scan.NewRegexScanner(nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	f := setup(t, WithScanner(callScanner{inner, &calls}))
	rows := []map[string]value.Value{f.memFields("first body"), f.memFields("second body")}
	start := calls.Load()                                           // setup's own writes
	f.eng.Prescan(context.Background(), schema.TableMemories, rows) // no memo: nothing
	if calls.Load() != start {
		t.Fatalf("Prescan without a memo scanned %d texts", calls.Load()-start)
	}
	ctx := WithScanMemo(context.Background())
	f.eng.Prescan(ctx, schema.TableMemories, rows)
	pre := calls.Load()
	if pre == 0 {
		t.Fatal("Prescan scanned nothing")
	}
	for _, r := range rows {
		if _, err := f.eng.Create(ctx, "", schema.TableMemories, r); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != pre {
		t.Fatalf("the creates scanned %d texts again after Prescan", got-pre)
	}
	if _, err := f.eng.Create(context.Background(), "", schema.TableMemories, f.memFields("third body")); err != nil || calls.Load() == pre {
		t.Fatalf("a create without the memo must scan: %v", err)
	}
}
