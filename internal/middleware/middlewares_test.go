package middleware

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	neoerrors "github.com/Protocol-Lattice/neo/internal/errors"
	"github.com/Protocol-Lattice/neo/internal/procedure"
)

func TestApplyMiddlewaresWrapsInRegistrationOrder(t *testing.T) {
	var calls []string

	first := func(next Handler) Handler {
		return func(ctx context.Context, input any) (any, error) {
			calls = append(calls, "first:before")
			out, err := next(ctx, input)
			calls = append(calls, "first:after")
			return out, err
		}
	}
	second := func(next Handler) Handler {
		return func(ctx context.Context, input any) (any, error) {
			calls = append(calls, "second:before")
			out, err := next(ctx, input)
			calls = append(calls, "second:after")
			return out, err
		}
	}

	handler := Apply([]Middleware{first, second}, func(ctx context.Context, input any) (any, error) {
		calls = append(calls, "handler")
		return "ok", nil
	})

	got, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("result = %v, want ok", got)
	}

	want := []string{"first:before", "second:before", "handler", "second:after", "first:after"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestApplyMiddlewaresPropagatesErrors(t *testing.T) {
	wantErr := errors.New("stop")
	middleware := func(next Handler) Handler {
		return func(ctx context.Context, input any) (any, error) {
			return nil, wantErr
		}
	}

	handler := Apply([]Middleware{middleware}, func(ctx context.Context, input any) (any, error) {
		t.Fatal("handler should not be called")
		return nil, nil
	})

	_, err := handler(context.Background(), nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestRecoverConvertsPanicToError(t *testing.T) {
	handler := Apply([]Middleware{Recover()}, func(ctx context.Context, input any) (any, error) {
		panic("boom")
	})

	got, err := handler(context.Background(), nil)
	if err == nil {
		t.Fatal("error is nil, want panic recovery error")
	}
	if got != nil {
		t.Fatalf("result = %v, want nil", got)
	}
	if !strings.Contains(err.Error(), "panic recovered: boom") {
		t.Fatalf("error = %v, want recovered panic detail", err)
	}
}

func TestRecoverPreservesReturnedError(t *testing.T) {
	wantErr := errors.New("handler failed")
	handler := Apply([]Middleware{Recover()}, func(ctx context.Context, input any) (any, error) {
		return nil, wantErr
	})

	_, err := handler(context.Background(), nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestObserveReportsSuccess(t *testing.T) {
	var got Observation
	handler := Apply([]Middleware{Observe(func(ctx context.Context, observation Observation) {
		got = observation
	})}, func(ctx context.Context, input any) (any, error) {
		return "ok", nil
	})

	ctx := WithObservation(context.Background(), Observation{
		Procedure: "user.get",
		Kind:      procedure.ProcedureKindQuery,
	})
	out, err := handler(ctx, nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %v, want ok", out)
	}
	if got.Procedure != "user.get" || got.Kind != procedure.ProcedureKindQuery {
		t.Fatalf("observation = %#v, want procedure metadata", got)
	}
	if got.Duration <= 0 {
		t.Fatalf("duration = %s, want positive", got.Duration)
	}
	if got.Error != nil || got.Code != "" {
		t.Fatalf("observation error = %v code = %q, want success", got.Error, got.Code)
	}
}

func TestObserveReportsErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code neoerrors.ErrorCode
	}{
		{
			name: "plain error",
			err:  errors.New("database failed"),
			code: neoerrors.CodeInternal,
		},
		{
			name: "coded error",
			err:  neoerrors.NewError(neoerrors.CodeNotFound, "missing"),
			code: neoerrors.CodeNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Observation
			handler := Apply([]Middleware{Observe(func(ctx context.Context, observation Observation) {
				got = observation
			})}, func(ctx context.Context, input any) (any, error) {
				return nil, tt.err
			})

			_, err := handler(context.Background(), nil)
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
			if got.Error != tt.err {
				t.Fatalf("observed error = %v, want %v", got.Error, tt.err)
			}
			if got.Code != tt.code {
				t.Fatalf("code = %q, want %q", got.Code, tt.code)
			}
		})
	}
}

func TestObserveReportsAndReraisesPanic(t *testing.T) {
	var got Observation
	handler := Apply([]Middleware{Observe(func(ctx context.Context, observation Observation) {
		got = observation
	})}, func(ctx context.Context, input any) (any, error) {
		panic("boom")
	})

	defer func() {
		recovered := recover()
		if recovered != "boom" {
			t.Fatalf("panic = %v, want boom", recovered)
		}
		if got.Error == nil {
			t.Fatal("observed error is nil, want panic error")
		}
		if got.Code != neoerrors.CodeInternal {
			t.Fatalf("code = %q, want %q", got.Code, neoerrors.CodeInternal)
		}
	}()

	_, _ = handler(context.Background(), nil)
}

func TestObserveAllowsNilObserver(t *testing.T) {
	handler := Apply([]Middleware{Observe(nil)}, func(ctx context.Context, input any) (any, error) {
		return "ok", nil
	})

	out, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %v, want ok", out)
	}
}

func TestRateLimitRejectsCallsPastLimitWithinWindow(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	calls := 0
	handler := Apply([]Middleware{rateLimitWithClock(2, time.Minute, func() time.Time {
		return now
	})}, func(ctx context.Context, input any) (any, error) {
		calls++
		return "ok", nil
	})

	for i := 0; i < 2; i++ {
		out, err := handler(context.Background(), nil)
		if err != nil {
			t.Fatalf("call %d error: %v", i+1, err)
		}
		if out != "ok" {
			t.Fatalf("call %d out = %v, want ok", i+1, out)
		}
	}

	out, err := handler(context.Background(), nil)
	if out != nil {
		t.Fatalf("limited output = %v, want nil", out)
	}
	if err == nil {
		t.Fatal("limited error is nil, want rate limit error")
	}
	normalized := neoerrors.Normalize(err)
	if normalized.Error.Code != neoerrors.CodeTooManyRequests {
		t.Fatalf("code = %q, want %q", normalized.Error.Code, neoerrors.CodeTooManyRequests)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestRateLimitResetsAfterWindow(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	handler := Apply([]Middleware{rateLimitWithClock(1, time.Minute, func() time.Time {
		return now
	})}, func(ctx context.Context, input any) (any, error) {
		return "ok", nil
	})

	if _, err := handler(context.Background(), nil); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if _, err := handler(context.Background(), nil); err == nil {
		t.Fatal("second call error is nil, want rate limit error")
	}

	now = now.Add(time.Minute)
	out, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("call after reset error: %v", err)
	}
	if out != "ok" {
		t.Fatalf("call after reset out = %v, want ok", out)
	}
}

func TestRateLimitDefaultsToProcedureKey(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	handler := Apply([]Middleware{rateLimitWithClock(1, time.Minute, func() time.Time {
		return now
	})}, func(ctx context.Context, input any) (any, error) {
		return "ok", nil
	})
	first := WithObservation(context.Background(), Observation{
		Procedure: "user.get",
		Kind:      procedure.ProcedureKindQuery,
	})
	second := WithObservation(context.Background(), Observation{
		Procedure: "user.create",
		Kind:      procedure.ProcedureKindMutation,
	})

	if _, err := handler(first, nil); err != nil {
		t.Fatalf("first procedure call error: %v", err)
	}
	if _, err := handler(second, nil); err != nil {
		t.Fatalf("second procedure call error: %v", err)
	}
	if _, err := handler(first, nil); err == nil {
		t.Fatal("repeated first procedure error is nil, want rate limit error")
	}
}

func TestRateLimitUsesCustomKey(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	handler := Apply([]Middleware{rateLimitWithClock(
		1,
		time.Minute,
		func() time.Time { return now },
		WithRateLimitKey(func(ctx context.Context, input any) string {
			key, _ := input.(string)
			return key
		}),
	)}, func(ctx context.Context, input any) (any, error) {
		return "ok", nil
	})

	if _, err := handler(context.Background(), "a"); err != nil {
		t.Fatalf("first key error: %v", err)
	}
	if _, err := handler(context.Background(), "b"); err != nil {
		t.Fatalf("second key error: %v", err)
	}
	if _, err := handler(context.Background(), "a"); err == nil {
		t.Fatal("repeated first key error is nil, want rate limit error")
	}
}

func TestCloneMiddlewaresReturnsIndependentSlice(t *testing.T) {
	first := func(next Handler) Handler { return next }
	second := func(next Handler) Handler { return next }

	original := []Middleware{first}
	cloned := Clone(original)
	original[0] = second

	if len(cloned) != 1 {
		t.Fatalf("len cloned = %d, want 1", len(cloned))
	}
	if reflect.ValueOf(cloned[0]).Pointer() != reflect.ValueOf(first).Pointer() {
		t.Fatal("clone changed after original slice mutation")
	}
}

func TestAppendMiddlewaresKeepsOrder(t *testing.T) {
	first := func(next Handler) Handler { return next }
	second := func(next Handler) Handler { return next }
	third := func(next Handler) Handler { return next }

	out := Append([]Middleware{first}, []Middleware{second, third})
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}

	want := []Middleware{first, second, third}
	for i := range want {
		if reflect.ValueOf(out[i]).Pointer() != reflect.ValueOf(want[i]).Pointer() {
			t.Fatalf("middleware at index %d changed", i)
		}
	}
}
