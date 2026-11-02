package teamharness

import "sync"

// Simultaneously starts every prepared command at the same instant (all
// goroutines wait on one barrier, then call Start together) and waits for
// all of them. This is how two lore processes "write in the same second" or
// N agents race `lore task next`: real processes, real git, no mocks.
func (w *World) Simultaneously(ps ...*Pending) []Result {
	w.T.Helper()
	gate := make(chan struct{})
	var ready, done sync.WaitGroup
	errs := make([]error, len(ps))
	for i, p := range ps {
		ready.Add(1)
		done.Add(1)
		go func(i int, p *Pending) {
			defer done.Done()
			ready.Done()
			<-gate
			if err := p.cmd.Start(); err != nil {
				p.started = err
				return
			}
			errs[i] = p.cmd.Wait()
		}(i, p)
	}
	ready.Wait()
	close(gate)
	done.Wait()
	out := make([]Result, len(ps))
	for i, p := range ps {
		if p.started != nil {
			w.T.Fatalf("start lore %v: %v", p.args, p.started)
		}
		out[i] = w.finish(w.Bin, p.args, errs[i], p.out.String(), p.errb.String())
	}
	return out
}
