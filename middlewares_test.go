package neo

import (
	"context"
	"errors"
	"reflect"
	"testing"
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

	handler := applyMiddlewares([]Middleware{first, second}, func(ctx context.Context, input any) (any, error) {
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

	handler := applyMiddlewares([]Middleware{middleware}, func(ctx context.Context, input any) (any, error) {
		t.Fatal("handler should not be called")
		return nil, nil
	})

	_, err := handler(context.Background(), nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestCloneMiddlewaresReturnsIndependentSlice(t *testing.T) {
	first := func(next Handler) Handler { return next }
	second := func(next Handler) Handler { return next }

	original := []Middleware{first}
	cloned := cloneMiddlewares(original)
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

	out := appendMiddlewares([]Middleware{first}, []Middleware{second, third})
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
