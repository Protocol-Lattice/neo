package procedure

import "context"

// ensureContext keeps defensive framework paths from blocking or panicking when
// a caller violates the Go convention that contexts must be non-nil.
func EnsureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
