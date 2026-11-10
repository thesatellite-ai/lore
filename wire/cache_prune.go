package wire

// LoreHome is the lore home this App was built with (~/.lore), for the
// Local operations that work on its folders (opscache.Env, `lore cache
// prune`, P3-MISC-2).
func (a *App) LoreHome() string { return a.opts.Home }
