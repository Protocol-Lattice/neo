package middleware

import (
	"context"
	"fmt"
	"runtime/debug"
	"slices"
	"time"

	neoerrors "github.com/Protocol-Lattice/neo/internal/errors"
	"github.com/Protocol-Lattice/neo/internal/procedure"
)

type Handler func(ctx context.Context, input any) (any, error)

type Middleware func(next Handler) Handler

// Observation describes one completed procedure handler call.
type Observation struct {
	Procedure string
	Kind      procedure.ProcedureKind
	Duration  time.Duration
	Error     error
	Code      neoerrors.ErrorCode
}

// Observer receives procedure observations from Observe middleware.
type Observer func(context.Context, Observation)

type observationContextKey struct{}

func WithObservation(ctx context.Context, observation Observation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, observationContextKey{}, observation)
}

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

// Observe reports procedure duration and normalized error status.
func Observe(observer Observer) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, input any) (out any, err error) {
			if ctx == nil {
				ctx = context.Background()
			}

			start := time.Now()
			var panicValue any
			defer func() {
				if recovered := recover(); recovered != nil {
					panicValue = recovered
					err = fmt.Errorf("panic observed: %v", recovered)
				}
				notifyObserver(ctx, observer, time.Since(start), err)
				if panicValue != nil {
					panic(panicValue)
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

func notifyObserver(ctx context.Context, observer Observer, duration time.Duration, err error) {
	if observer == nil {
		return
	}

	observation := observationFromContext(ctx)
	observation.Duration = duration
	observation.Error = err
	observation.Code = ""
	if err != nil {
		observation.Code = neoerrors.Normalize(err).Error.Code
	}
	observer(ctx, observation)
}

func observationFromContext(ctx context.Context) Observation {
	if ctx == nil {
		return Observation{}
	}
	observation, _ := ctx.Value(observationContextKey{}).(Observation)
	return observation
}
