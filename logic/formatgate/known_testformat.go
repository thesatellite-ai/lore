//go:build loretest_format

package formatgate

import "github.com/thesatellite-ai/lore/model/row"

// testFeatures: in a loretest_format build the test step's feature is
// known, so the migrate machinery can be proven end to end (rule 4).
var testFeatures = []Feature{{Name: row.TestFeature, Since: since018}}
