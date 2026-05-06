package replica

import (
	"context"
	"time"
)

// contextWithTimeoutShort returns a 5-second context — used by the
// rules reloader, which is on the request hot path.
func contextWithTimeoutShort() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
