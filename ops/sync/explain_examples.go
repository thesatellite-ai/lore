package sync

import (
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// explainSpec completes `lore explain`'s spec: its --help example (every
// knowledge command shows at least one runnable line, campaign knowledge
// 2026-11-08; explain is the one a person reaches for when a row looks
// missing or stale) and E_INVALID_ID, which a typo'd id answers instead
// of a "found nowhere" report.
func explainSpec(s op.Spec) op.Spec {
	s.Examples = []op.Example{
		{Title: "why does this row look the way it does", CLI: "lore explain mem_<id>", Want: "mem_"},
	}
	s.Errors = append(append([]errs.Code{}, s.Errors...), errs.InvalidID)
	return s
}
