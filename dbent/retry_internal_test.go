package dbent

import (
	"errors"
	"testing"
)

func TestRetryOnSchemaChange(t *testing.T) {
	t.Parallel()
	schemaChanged := errors.New("quick_check: " + sqliteSchemaChanged + " (17)")
	for _, tc := range []struct {
		name      string
		failures  int   // calls that fail before success
		failWith  error // the error those calls return
		wantCalls int
		wantErr   bool
	}{
		{"succeeds at once", 0, nil, 1, false},
		{"schema change then success", 2, schemaChanged, 3, false},
		{"schema change every time", quickCheckAttempts + 5, schemaChanged, quickCheckAttempts, true},
		{"other errors are not retried", 3, errors.New("disk I/O error"), 1, true},
	} {
		calls := 0
		err := retryOnSchemaChange(func() error {
			calls++
			if calls <= tc.failures {
				return tc.failWith
			}
			return nil
		})
		if calls != tc.wantCalls || (err != nil) != tc.wantErr {
			t.Fatalf("%s: %d calls, err %v; want %d calls, err %v", tc.name, calls, err, tc.wantCalls, tc.wantErr)
		}
	}
}
