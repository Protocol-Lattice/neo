package neo

import (
	"context"
	"strings"
	"testing"
	"time"
)

type procedureInput struct {
	Name string `json:"name"`
}

type procedureOutput struct {
	Message string `json:"message"`
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
	got, err := decodeInput[procedureInput](nil)
	if err != nil {
		t.Fatalf("decode nil: %v", err)
	}
	if got != (procedureInput{}) {
		t.Fatalf("got = %#v, want zero value", got)
	}
}

func TestDecodeInputRejectsInvalidShape(t *testing.T) {
	_, err := decodeInput[procedureInput](map[string]any{"name": map[string]any{"nested": true}})
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

func TestSubscriptionProcedureRejectsInvalidFunction(t *testing.T) {
	procedure := Subscription[procedureInput, procedureOutput](func(ctx context.Context, input procedureInput) (<-chan procedureOutput, error) {
		return nil, nil
	})

	_, err := procedure.Call(context.Background(), "not-a-function", procedureInput{})
	if err == nil || !strings.Contains(err.Error(), "invalid subscription function") {
		t.Fatalf("error = %v, want invalid subscription function", err)
	}
}
