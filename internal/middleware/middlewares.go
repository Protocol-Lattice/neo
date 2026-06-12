package middleware

import (
	"context"
	"fmt"
	"runtime/debug"
	"slices"
	"sync"
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

// RateLimitKeyFunc returns the bucket key used by RateLimit.
type RateLimitKeyFunc func(context.Context, any) string

type rateLimitConfig struct {
	key RateLimitKeyFunc
}

// RateLimitOption customizes RateLimit.
type RateLimitOption func(*rateLimitConfig)

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

// WithRateLimitKey customizes which calls share a RateLimit bucket.
func WithRateLimitKey(key RateLimitKeyFunc) RateLimitOption {
	return func(config *rateLimitConfig) {
		if key != nil {
			config.key = key
		}
	}
}

// RateLimit rejects calls after limit is reached within window.
func RateLimit(limit int, window time.Duration, opts ...RateLimitOption) Middleware {
	return rateLimitWithClock(limit, window, time.Now, opts...)
}

func rateLimitWithClock(limit int, window time.Duration, now func() time.Time, opts ...RateLimitOption) Middleware {
	if limit <= 0 || window <= 0 {
		return func(next Handler) Handler {
			return next
		}
	}
	if now == nil {
		now = time.Now
	}

	config := rateLimitConfig{
		key: defaultRateLimitKey,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}

	limiter := &rateLimiter{
		limit:   limit,
		window:  window,
		now:     now,
		key:     config.key,
		buckets: make(map[string]rateLimitBucket),
	}

	return func(next Handler) Handler {
		return func(ctx context.Context, input any) (any, error) {
			if !limiter.allow(limiter.key(ctx, input)) {
				return nil, neoerrors.NewError(neoerrors.CodeTooManyRequests, "rate limit exceeded")
			}

			return next(ctx, input)
		}
	}
}

type rateLimiter struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	now         func() time.Time
	key         RateLimitKeyFunc
	buckets     map[string]rateLimitBucket
	lastCleanup time.Time
}

type rateLimitBucket struct {
	windowStart time.Time
	count       int
}

func (limiter *rateLimiter) allow(key string) bool {
	now := limiter.now()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	limiter.cleanupExpiredBuckets(now)

	bucket := limiter.buckets[key]
	if bucket.windowStart.IsZero() || now.Sub(bucket.windowStart) >= limiter.window || now.Before(bucket.windowStart) {
		bucket = rateLimitBucket{windowStart: now}
	}
	if bucket.count >= limiter.limit {
		limiter.buckets[key] = bucket
		return false
	}

	bucket.count++
	limiter.buckets[key] = bucket
	return true
}

func (limiter *rateLimiter) cleanupExpiredBuckets(now time.Time) {
	if limiter.lastCleanup.IsZero() {
		limiter.lastCleanup = now
		return
	}
	if now.Sub(limiter.lastCleanup) < limiter.window && !now.Before(limiter.lastCleanup) {
		return
	}

	for key, bucket := range limiter.buckets {
		if bucket.windowStart.IsZero() || now.Sub(bucket.windowStart) >= limiter.window || now.Before(bucket.windowStart) {
			delete(limiter.buckets, key)
		}
	}
	limiter.lastCleanup = now
}

func defaultRateLimitKey(ctx context.Context, input any) string {
	observation := observationFromContext(ctx)
	if observation.Procedure == "" && observation.Kind == "" {
		return ""
	}
	return string(observation.Kind) + ":" + observation.Procedure
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
