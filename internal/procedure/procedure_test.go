package procedure

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	neoerrors "github.com/Protocol-Lattice/neo/internal/errors"
)

type procedureInput struct {
	Name string `json:"name"`
}

type procedureOutput struct {
	Message string `json:"message"`
}

type validatedProcedureInput struct {
	Name string `json:"name"`
}

func (input validatedProcedureInput) Validate() error {
	if input.Name == "" {
		return errors.New("name is required")
	}
	return nil
}

func TestQueryDecodesMapInputAndReturnsTypedOutput(t *testing.T) {
	procedure := Query[procedureInput, procedureOutput](func(ctx context.Context, input procedureInput) (procedureOutput, error) {
		return procedureOutput{Message: "hello " + input.Name}, nil
	})

	got, err := procedure.Call(context.Background(), procedure.Fn, map[string]any{"name": "Neo"})
	if err != nil {
		t.Fatalf("call query: %v", err)
	}

	out, ok := got.(procedureOutput)
	if !ok {
		t.Fatalf("output type = %T, want procedureOutput", got)
	}
	if out.Message != "hello Neo" {
		t.Fatalf("message = %q, want hello Neo", out.Message)
	}
	if procedure.Kind != ProcedureKindQuery || procedure.Meta.Kind != ProcedureKindQuery {
		t.Fatalf("unexpected query metadata: %#v", procedure.Meta)
	}
}

func TestProcedureOptionsPopulateMetadata(t *testing.T) {
	procedure := Query[procedureInput, procedureOutput](
		func(ctx context.Context, input procedureInput) (procedureOutput, error) {
			return procedureOutput{}, nil
		},
		WithSummary("Get a greeting"),
		WithDescription("Returns a greeting for a name."),
		WithTags("greeting", "example"),
		WithDeprecated(),
	)

	if procedure.Meta.Summary != "Get a greeting" {
		t.Fatalf("summary = %q, want Get a greeting", procedure.Meta.Summary)
	}
	if procedure.Meta.Description != "Returns a greeting for a name." {
		t.Fatalf("description = %q", procedure.Meta.Description)
	}
	if strings.Join(procedure.Meta.Tags, ",") != "greeting,example" {
		t.Fatalf("tags = %#v", procedure.Meta.Tags)
	}
	if !procedure.Meta.Deprecated {
		t.Fatal("deprecated = false, want true")
	}
}

func TestProcedureValidatesDecodedInput(t *testing.T) {
	procedure := Mutation[validatedProcedureInput, procedureOutput](func(ctx context.Context, input validatedProcedureInput) (procedureOutput, error) {
		return procedureOutput{Message: input.Name}, nil
	})

	_, err := procedure.Call(context.Background(), procedure.Fn, map[string]any{"name": ""})
	var neoErr *neoerrors.Error
	if !errors.As(err, &neoErr) {
		t.Fatalf("error = %v, want *Error", err)
	}
	if neoErr.Code != neoerrors.CodeBadRequest || !strings.Contains(neoErr.Unwrap().Error(), "name is required") {
		t.Fatalf("error = %#v, cause=%v; want bad request validation error", neoErr, neoErr.Unwrap())
	}
}

func TestMutationUsesMutationKindMetadata(t *testing.T) {
	procedure := Mutation[procedureInput, procedureOutput](func(ctx context.Context, input procedureInput) (procedureOutput, error) {
		return procedureOutput{Message: "created " + input.Name}, nil
	})

	if procedure.Kind != ProcedureKindMutation {
		t.Fatalf("kind = %q, want mutation", procedure.Kind)
	}
	if procedure.Meta.Kind != ProcedureKindMutation {
		t.Fatalf("meta kind = %q, want mutation", procedure.Meta.Kind)
	}
	if procedure.Meta.Input != "procedureInput" || procedure.Meta.Output != "procedureOutput" {
		t.Fatalf("unexpected meta types: %#v", procedure.Meta)
	}
}

func TestProcedureCallRejectsInvalidFunction(t *testing.T) {
	procedure := Query[procedureInput, procedureOutput](func(ctx context.Context, input procedureInput) (procedureOutput, error) {
		return procedureOutput{}, nil
	})

	_, err := procedure.Call(context.Background(), "not-a-function", procedureInput{})
	if err == nil || !strings.Contains(err.Error(), "invalid procedure function") {
		t.Fatalf("error = %v, want invalid procedure function", err)
	}
}

func TestDecodeInputNilReturnsZeroValue(t *testing.T) {
	got, err := DecodeInput[procedureInput](nil)
	if err != nil {
		t.Fatalf("decode nil: %v", err)
	}
	if got != (procedureInput{}) {
		t.Fatalf("got = %#v, want zero value", got)
	}
}

func TestDecodeInputRejectsInvalidShape(t *testing.T) {
	_, err := DecodeInput[procedureInput](map[string]any{"name": map[string]any{"nested": true}})
	if err == nil || !strings.Contains(err.Error(), "decode typed input") {
		t.Fatalf("error = %v, want decode typed input error", err)
	}
}

func TestSubscriptionProcedureStreamsValues(t *testing.T) {
	procedure := Subscription[procedureInput, procedureOutput](func(ctx context.Context, input procedureInput) (<-chan procedureOutput, error) {
		out := make(chan procedureOutput, 1)
		out <- procedureOutput{Message: "event " + input.Name}
		close(out)
		return out, nil
	})

	stream, err := procedure.Call(context.Background(), procedure.Fn, map[string]any{"name": "Neo"})
	if err != nil {
		t.Fatalf("call subscription: %v", err)
	}

	select {
	case raw, ok := <-stream:
		if !ok {
			t.Fatal("stream closed before value")
		}
		got, ok := raw.(procedureOutput)
		if !ok {
			t.Fatalf("stream value type = %T, want procedureOutput", raw)
		}
		if got.Message != "event Neo" {
			t.Fatalf("message = %q, want event Neo", got.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscription value")
	}
}

func TestSubscriptionProcedureClosesNilStream(t *testing.T) {
	procedure := Subscription[procedureInput, procedureOutput](func(ctx context.Context, input procedureInput) (<-chan procedureOutput, error) {
		return nil, nil
	})

	stream, err := procedure.Call(context.Background(), procedure.Fn, procedureInput{})
	if err != nil {
		t.Fatalf("call subscription: %v", err)
	}

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("stream is open, want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for nil subscription stream to close")
	}
}

func TestSubscriptionProcedureStopsWaitingOnContextCancel(t *testing.T) {
	quiet := make(chan procedureOutput)
	procedure := Subscription[procedureInput, procedureOutput](func(ctx context.Context, input procedureInput) (<-chan procedureOutput, error) {
		return quiet, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := procedure.Call(ctx, procedure.Fn, procedureInput{})
	if err != nil {
		t.Fatalf("call subscription: %v", err)
	}

	cancel()

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("stream is open, want closed after context cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled subscription stream to close")
	}
}

func TestSubscriptionProcedureRejectsInvalidFunction(t *testing.T) {
	procedure := Subscription[procedureInput, procedureOutput](func(ctx context.Context, input procedureInput) (<-chan procedureOutput, error) {
		return nil, nil
	})

	_, err := procedure.Call(context.Background(), "not-a-function", procedureInput{})
	if err == nil || !strings.Contains(err.Error(), "invalid subscription function") {
		t.Fatalf("error = %v, want invalid subscription function", err)
	}
}
