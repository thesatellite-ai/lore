package wire

import "time"

// Now is the wall clock the engine stamps rows with (Options.Wall), so an
// op's own time stamps (archived_at, taken_at, done_at, a reminder's due
// check) agree with the row's HLC and created_at. It satisfies
// ops/internal/opclock.Clocker structurally: that package is internal to
// ops/, so wire cannot name it in an interface assertion (Go's internal
// rule); TestNowIsTheEngineWall pins the method instead.
func (a *App) Now() time.Time { return a.opts.Wall() }
