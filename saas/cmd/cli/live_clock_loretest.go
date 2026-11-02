//go:build loretest

package main

import (
	"os"
	"time"
)

// envNow fixes the clock in test builds (§1: LORE_NOW, test builds only).
const envNow = "LORE_NOW"

// liveNowOverride reads LORE_NOW (RFC 3339).
func liveNowOverride() (time.Time, bool) {
	v := os.Getenv(envNow)
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}
