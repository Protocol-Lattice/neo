package middleware

import (
	"context"
	"fmt"
	"runtime/debug"
	"slices"
)

type Handler func(ctx context.Context, input any) (any, error)

type Middleware func(next Handler) Handler

// Recover converts panics from downstream handlers into ordinary errors.
func Recover() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, input any) (out any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					out = nil
					err = fmt.Errorf("panic recovered: %v\n%s", recovered, debug.Stack())
				}
			}()

			return next(ctx, input)
		}
	}
}

func Apply(middlewares []Middleware, handler Handler) Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}

	return handler
}

func Clone(middlewares []Middleware) []Middleware {
	return slices.Clone(middlewares)
}

func Append(first []Middleware, second []Middleware) []Middleware {
	return slices.Concat(first, second)
}
