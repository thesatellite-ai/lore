package leakcheck

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

// parallelWait bounds how long the first secret scan waits for a second
// one to start: a Check that scans items one at a time never starts it.
const parallelWait = 5 * time.Second

// TestCheckScansItemsInParallel: C-9, `lore check leaks --as-client`
// scanned 10 000 tracked files for secrets on one core (70 s on a big
// repo). Check runs items on every CPU: two scans are in flight at once,
// and the findings are the same, in the same order, as item by item.
func TestCheckScansItemsInParallel(t *testing.T) {
	t.Parallel()
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs two CPUs")
	}
	items := make([]Item, 40)
	for i := range items {
		p := fmt.Sprintf("src/file%02d.go", i)
		items[i] = Item{Where: p, Path: p, Content: []byte("package x\n")}
	}
	var once sync.Once
	second := make(chan struct{})
	var mu sync.Mutex
	started := 0
	overlap := false
	scan := func(it Item) []SecretHit {
		mu.Lock()
		started++
		first := started == 1
		if started == 2 {
			close(second)
		}
		mu.Unlock()
		if first {
			select {
			case <-second:
				once.Do(func() { overlap = true })
			case <-time.After(parallelWait):
			}
		}
		return []SecretHit{{Line: 1, Rule: "test-rule", Preview: "ab…", Field: "", Hash: uint32(len(it.Path))}}
	}
	got := Check(items, nil, Options{Secrets: true, SecretScan: scan})
	if !overlap {
		t.Fatalf("no second secret scan started while the first ran: Check scans one item at a time")
	}
	var want []Finding
	for _, it := range items {
		want = append(want, Finding{Rule: RuleSecret, Where: it.Where, Path: it.Path, Line: 1, Detail: "credential pattern test-rule (ab…)"})
	}
	sortFindings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findings differ from an item-by-item check:\ngot  %+v\nwant %+v", got, want)
	}
}
