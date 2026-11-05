// closer.go — fold a deferred Close error into a function's named error
// result, so a failed close (a DB, a rows cursor, a written file) is
// reported instead of dropped.
package main

import (
	"errors"
	"io"

	"saas/pkg/aicoder/errcodes"
)

// joinClose closes c and joins its error, wrapped as E_INTERNAL naming what,
// into *errp. Use it as `defer joinClose(&err, db, "the project DB")` in a
// function with a named `err error` result. The first error keeps its code:
// errors.As walks the joined errors in order.
func joinClose(errp *error, c io.Closer, what string) {
	if cerr := c.Close(); cerr != nil {
		*errp = errors.Join(*errp, errcodes.New(errcodes.Internal, "close "+what).WithCause(cerr))
	}
}
