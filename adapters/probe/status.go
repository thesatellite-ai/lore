package probe

import "github.com/thesatellite-ai/lore/logic/onboard"

// Status maps a probe class to the check status of ONB 2.2 before the
// check's level is applied: ok is ok; a probe that could not run (timeout,
// tool missing, bad input) is error; every other class is fail. The op
// turns fail into warn for a warn-level check and decides skip.
func (c Class) Status() onboard.Status {
	switch {
	case c == ClassOK:
		return onboard.StatusOK
	case !c.Ran():
		return onboard.StatusError
	}
	return onboard.StatusFail
}
