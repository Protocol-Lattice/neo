package middleware

import (
	"context"
	"slices"
)

type Handler func(ctx context.Context, input any) (any, error)

type Middleware func(next Handler) Handler

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
