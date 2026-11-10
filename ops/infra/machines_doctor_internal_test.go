package infra

import (
	"slices"
	"testing"
)

// TestSharedMachines: one id with two hosts across stores is reported;
// this machine's current host counts only for an id some store holds;
// empty ids and hosts (ephemeral runtimes) never count (6.8, OQ7).
func TestSharedMachines(t *testing.T) {
	const a, b = "mch_aaaa", "mch_bbbb"
	cases := []struct {
		name string
		seen []idHost
		mine idHost
		want []SharedMachine
	}{
		{"one host per id", []idHost{{a, "h1"}, {a, "h1"}, {b, "h2"}}, idHost{}, nil},
		{"clone across stores", []idHost{{a, "h1"}, {a, "h2"}}, idHost{}, []SharedMachine{{ID: a, Hosts: []string{"h1", "h2"}}}},
		{"own row names another host", []idHost{{a, "h1"}}, idHost{a, "h9"}, []SharedMachine{{ID: a, Hosts: []string{"h1", "h9"}, This: true}}},
		{"own id without a profile", []idHost{{b, "h1"}}, idHost{a, "h9"}, nil},
		{"empty host never counts", []idHost{{a, ""}, {a, "h1"}, {"", "h2"}}, idHost{a, ""}, nil},
		{"sorted by id", []idHost{{b, "x"}, {b, "y"}, {a, "y"}, {a, "x"}}, idHost{},
			[]SharedMachine{{ID: a, Hosts: []string{"x", "y"}}, {ID: b, Hosts: []string{"x", "y"}}}},
	}
	for _, c := range cases {
		got := sharedMachines(c.seen, c.mine)
		if !slices.EqualFunc(got, c.want, func(x, y SharedMachine) bool {
			return x.ID == y.ID && x.This == y.This && slices.Equal(x.Hosts, y.Hosts)
		}) {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}
