package wire

// WaitAliasProbesForTest returns once every ssh probe a now-finished
// operation of a left running in the background has ended (the sweep
// goroutines aliasCache.bg counts). A setup check may return as soon as one
// alias logs in as the account while the others are still answering;
// their ssh calls belong to that check, and a test that counts the calls of
// the next operation waits here first so it never counts them against it.
func WaitAliasProbesForTest(a *App) {
	if a != nil && a.aliasCache != nil {
		a.aliasCache.bg.Wait()
	}
}
