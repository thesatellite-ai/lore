package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestEstimateSortAndWhereNumeric (TASK 9.1, later 429): the estimate is
// stored as its decimal text ("10.00"), but --sort estimate and
// --where estimate>N compare it as a number. A text compare would put
// "10" before "2.5" and "9", and estimate>3 would drop "10" ("1" < "3").
func TestEstimateSortAndWhereNumeric(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ten := add(h, "ten", `"estimate":"10"`)
	nine := add(h, "nine", `"estimate":"9"`)
	small := add(h, "small", `"estimate":"2.5"`)
	order := func(js string, want ...string) {
		t.Helper()
		last := -1
		for _, id := range want {
			at := strings.Index(js, `"`+id+`"`)
			if at < 0 || at < last {
				t.Fatalf("want order %v in:\n%s", want, js)
			}
			last = at
		}
	}
	cases := []struct {
		name  string
		input string
		want  []string
		gone  []string
	}{
		{"sort asc", `{"sort":["estimate:asc"]}`, []string{small, nine, ten}, nil},
		{"sort desc", `{"sort":["estimate:desc"]}`, []string{ten, nine, small}, nil},
		{"where > 3", `{"where":["estimate>3"],"sort":["estimate"]}`, []string{nine, ten}, []string{small}},
		{"where <= 9", `{"where":["estimate<=9"],"sort":["estimate"]}`, []string{small, nine}, []string{ten}},
		{"where = 10", `{"where":["estimate=10"]}`, []string{ten}, []string{small, nine}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			js := h.JSON(h.Must(opstask.OpList, c.input))
			order(js, c.want...)
			for _, id := range c.gone {
				if strings.Contains(js, `"`+id+`"`) {
					t.Fatalf("%s listed by %s:\n%s", id, c.input, js)
				}
			}
		})
	}
}
