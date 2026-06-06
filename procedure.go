package neo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

type ProcedureKind string

const (
	ProcedureKindQuery        ProcedureKind = "query"
	ProcedureKindMutation     ProcedureKind = "mutation"
	ProcedureKindSubscription ProcedureKind = "subscription"
)

type ProcedureMeta struct {
	Key    string        `json:"key"`
	Kind   ProcedureKind `json:"kind"`
	Input  string        `json:"input"`
	Output string        `json:"output"`
}

type Procedure[Fn, In, Out any] struct {
	Fn   Fn
	Kind ProcedureKind
	Meta ProcedureMeta
	Call func(ctx context.Context, fn Fn, args In) (Out, error)
}

type SubscriptionProcedure[Fn, In, Out any] struct {
	Fn   Fn
	Kind ProcedureKind
	Meta ProcedureMeta
	Call func(ctx context.Context, fn Fn, args In) (<-chan Out, error)
}

func Query[In, Out any](fn func(context.Context, In) (Out, error)) *Procedure[any, any, any] {
	return typedProcedure(ProcedureKindQuery, fn)
}

func Mutation[In, Out any](fn func(context.Context, In) (Out, error)) *Procedure[any, any, any] {
	return typedProcedure(ProcedureKindMutation, fn)
}

func Subscription[In, Out any](fn func(context.Context, In) (<-chan Out, error)) *SubscriptionProcedure[any, any, any] {
	return &SubscriptionProcedure[any, any, any]{
		Fn:   fn,
		Kind: ProcedureKindSubscription,
		Meta: ProcedureMeta{
			Kind:   ProcedureKindSubscription,
			Input:  typeName[In](),
			Output: typeName[Out](),
		},
		Call: func(ctx context.Context, rawFn any, input any) (<-chan any, error) {
			ctx = ensureContext(ctx)

			call, ok := rawFn.(func(context.Context, In) (<-chan Out, error))
			if !ok {
				return nil, errors.New("invalid subscription function")
			}

			decoded, err := decodeInput[In](input)
			if err != nil {
				return nil, err
			}

			stream, err := call(ctx, decoded)
			if err != nil {
				return nil, err
			}

			return mapStream(ctx, stream, func(value Out) (any, bool) {
				return value, true
			}), nil
		},
	}
}

func typedProcedure[In, Out any](kind ProcedureKind, fn func(context.Context, In) (Out, error)) *Procedure[any, any, any] {
	return &Procedure[any, any, any]{
		Fn:   fn,
		Kind: kind,
		Meta: ProcedureMeta{
			Kind:   kind,
			Input:  typeName[In](),
			Output: typeName[Out](),
		},
		Call: func(ctx context.Context, rawFn any, input any) (any, error) {
			ctx = ensureContext(ctx)

			call, ok := rawFn.(func(context.Context, In) (Out, error))
			if !ok {
				return nil, errors.New("invalid procedure function")
			}

			decoded, err := decodeInput[In](input)
			if err != nil {
				return nil, err
			}

			return call(ctx, decoded)
		},
	}
}

func decodeInput[T any](input any) (T, error) {
	var zero T
	if input == nil {
		return zero, nil
	}

	if typed, ok := input.(T); ok {
		return typed, nil
	}

	raw, err := json.Marshal(input)
	if err != nil {
		return zero, fmt.Errorf("marshal typed input: %w", err)
	}

	if err := json.Unmarshal(raw, &zero); err != nil {
		return zero, fmt.Errorf("decode typed input into %s: %w", typeName[T](), err)
	}

	return zero, nil
}

func mapStream[In, Out any](ctx context.Context, in <-chan In, mapValue func(In) (Out, bool)) <-chan Out {
	ctx = ensureContext(ctx)

	out := make(chan Out)
	if in == nil {
		close(out)
		return out
	}

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case value, ok := <-in:
				if !ok {
					return
				}

				mapped, keepGoing := mapValue(value)
				if !keepGoing {
					return
				}

				select {
				case <-ctx.Done():
					return
				case out <- mapped:
				}
			}
		}
	}()

	return out
}

func typeName[T any]() string {
	t := reflect.TypeOf((*T)(nil)).Elem()
	if t.PkgPath() == "" {
		return t.String()
	}
	return t.Name()
}
