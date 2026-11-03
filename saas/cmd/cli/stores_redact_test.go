package main

import "testing"

// Q-stores-3: any common CI marker turns redaction on.
func TestInCIMarkers(t *testing.T) {
	for _, k := range ciMarkers {
		t.Setenv(k, "")
	}
	if inCI() {
		t.Fatal("no marker set")
	}
	for _, k := range ciMarkers {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, "1")
			if !inCI() || !quietOutput() {
				t.Fatalf("%s must count as CI", k)
			}
		})
	}
}
