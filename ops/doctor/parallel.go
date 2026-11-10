package doctor

import (
	"runtime"
	"sync"
)

// MaxParallelCap is the most cores one doctor run uses: its checks run at
// most this many at once, and `lore doctor` caps the Go scheduler to it
// (cmd/lore). Why: `lore doctor --all` on a 48-core machine ran every
// probe of every setup at once and kept 12 cores busy for a health check
// that is mostly waiting on git and ssh; four is the probe limit of ONB
// 6.1 too (adapters/probe.DefaultConcurrency), so no more checks run than
// may reach the network at once.
const MaxParallelCap = 4

// MaxParallel is min(MaxParallelCap, the machine's CPUs): how many checks
// one doctor run (CLI, REST, the setup page's probes) runs at once.
func MaxParallel() int {
	return max(1, min(MaxParallelCap, runtime.NumCPU()))
}

// Parallel runs work(0..n-1) on at most MaxParallel goroutines and returns
// when every call has returned. Each index runs once; work must be safe
// for concurrent use.
func Parallel(n int, work func(i int)) {
	workers := min(MaxParallel(), n)
	next := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				work(i)
			}
		}()
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}
