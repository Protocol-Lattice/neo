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

var jsonRawMessageType = reflect.TypeOf(json.RawMessage{})

type ProcedureMeta struct {
	Key         string        `json:"key"`
	Kind        ProcedureKind `json:"kind"`
	Input       string        `json:"input"`
	Output      string        `json:"output"`
	Summary     string        `json:"summary,omitempty"`
	Description string        `json:"description,omitempty"`
	Tags        []string      `json:"tags,omitempty"`
	Deprecated  bool          `json:"deprecated,omitempty"`
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

// ProcedureOption customizes metadata for generated clients, docs, and schema
// exports without changing procedure execution.
type ProcedureOption func(*ProcedureMeta)

// Validator is implemented by input types that can validate themselves after
// JSON decoding and before the handler runs.
type Validator interface {
	Validate() error
}

// WithSummary sets a short one-line procedure summary.
func WithSummary(summary string) ProcedureOption {
	return func(meta *ProcedureMeta) {
		meta.Summary = summary
	}
}

// WithDescription sets longer procedure documentation.
func WithDescription(description string) ProcedureOption {
	return func(meta *ProcedureMeta) {
		meta.Description = description
	}
}

// WithTags adds procedure tags used by generated docs and schema exports.
func WithTags(tags ...string) ProcedureOption {
	return func(meta *ProcedureMeta) {
		meta.Tags = append(meta.Tags, tags...)
	}
}

// WithDeprecated marks a procedure as deprecated in metadata.
func WithDeprecated() ProcedureOption {
	return func(meta *ProcedureMeta) {
		meta.Deprecated = true
	}
}

// WithProcedureMeta overlays metadata fields on a procedure. The registered key
// is still set by Router.Register or Router.RegisterSubscription; non-empty
// Kind, Input, Output, Summary, Description, Tags, and Deprecated fields here
// override or extend inferred metadata.
func WithProcedureMeta(meta ProcedureMeta) ProcedureOption {
	return func(target *ProcedureMeta) {
		mergeProcedureMeta(target, meta)
	}
}

func Query[In, Out any](fn func(context.Context, In) (Out, error), opts ...ProcedureOption) *Procedure[any, any, any] {
	return typedProcedure(ProcedureKindQuery, fn, opts...)
}

func Mutation[In, Out any](fn func(context.Context, In) (Out, error), opts ...ProcedureOption) *Procedure[any, any, any] {
	return typedProcedure(ProcedureKindMutation, fn, opts...)
}

func Subscription[In, Out any](fn func(context.Context, In) (<-chan Out, error), opts ...ProcedureOption) *SubscriptionProcedure[any, any, any] {
	meta := ProcedureMeta{
		Kind:   ProcedureKindSubscription,
		Input:  typeName[In](),
		Output: typeName[Out](),
	}
	applyProcedureOptions(&meta, opts)

	return &SubscriptionProcedure[any, any, any]{
		Fn:   fn,
		Kind: ProcedureKindSubscription,
		Meta: meta,
		Call: func(ctx context.Context, rawFn any, input any) (<-chan any, error) {
			ctx = ensureContext(ctx)

			call, ok := rawFn.(func(context.Context, In) (<-chan Out, error))
			if !ok {
				return nil, errors.New("invalid subscription function")
			}

			decoded, err := decodeInput[In](input)
			if err != nil {
				return nil, WrapError(CodeBadRequest, "invalid input", err)
			}
			if err := validateInput(decoded); err != nil {
				return nil, WrapError(CodeBadRequest, "invalid input", err)
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

func typedProcedure[In, Out any](kind ProcedureKind, fn func(context.Context, In) (Out, error), opts ...ProcedureOption) *Procedure[any, any, any] {
	meta := ProcedureMeta{
		Kind:   kind,
		Input:  typeName[In](),
		Output: typeName[Out](),
	}
	applyProcedureOptions(&meta, opts)

	return &Procedure[any, any, any]{
		Fn:   fn,
		Kind: kind,
		Meta: meta,
		Call: func(ctx context.Context, rawFn any, input any) (any, error) {
			ctx = ensureContext(ctx)

			call, ok := rawFn.(func(context.Context, In) (Out, error))
			if !ok {
				return nil, errors.New("invalid procedure function")
			}

			decoded, err := decodeInput[In](input)
			if err != nil {
				return nil, WrapError(CodeBadRequest, "invalid input", err)
			}
			if err := validateInput(decoded); err != nil {
				return nil, WrapError(CodeBadRequest, "invalid input", err)
			}

			return call(ctx, decoded)
		},
	}
}

func applyProcedureOptions(meta *ProcedureMeta, opts []ProcedureOption) {
	for _, opt := range opts {
		if opt != nil {
			opt(meta)
		}
	}
}

func mergeProcedureMeta(target *ProcedureMeta, source ProcedureMeta) {
	if source.Key != "" {
		target.Key = source.Key
	}
	if source.Kind != "" {
		target.Kind = source.Kind
	}
	if source.Input != "" {
		target.Input = source.Input
	}
	if source.Output != "" {
		target.Output = source.Output
	}
	if source.Summary != "" {
		target.Summary = source.Summary
	}
	if source.Description != "" {
		target.Description = source.Description
	}
	if len(source.Tags) > 0 {
		target.Tags = append(target.Tags, source.Tags...)
	}
	if source.Deprecated {
		target.Deprecated = true
	}
}

func validateInput[T any](input T) error {
	if validator, ok := any(input).(Validator); ok {
		return validator.Validate()
	}
	if validator, ok := any(&input).(Validator); ok {
		return validator.Validate()
	}
	return nil
}

func decodeInput[T any](input any) (T, error) {
	var zero T
	if input == nil {
		return zero, nil
	}

	if raw, ok := input.(json.RawMessage); ok {
		if len(raw) == 0 {
			return zero, nil
		}
		if reflect.TypeOf((*T)(nil)).Elem() == jsonRawMessageType {
			return any(raw).(T), nil
		}
		if err := json.Unmarshal(raw, &zero); err != nil {
			return zero, fmt.Errorf("decode typed input into %s: %w", typeName[T](), err)
		}
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
