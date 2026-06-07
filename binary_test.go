package neo

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBinaryCodecRoundTripsTypedEnvelope(t *testing.T) {
	type input struct {
		Name  string `json:"name,omitempty"`
		Count int    `json:"count,string,omitempty"`
		Skip  string `json:"-"`
	}

	raw, err := NeoBinaryCodec.Marshal(typedRequest[input]{
		Input: input{Name: "Neo", Count: 7, Skip: "secret"},
	})
	if err != nil {
		t.Fatalf("marshal binary: %v", err)
	}

	var req typedRequest[input]
	if err := NeoBinaryCodec.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal binary: %v", err)
	}
	if req.Input.Name != "Neo" || req.Input.Count != 7 || req.Input.Skip != "" {
		t.Fatalf("request = %#v, want decoded input without skipped field", req)
	}
}

func TestReadInputPOSTDecodesBinaryBody(t *testing.T) {
	raw, err := NeoBinaryCodec.Marshal(Request{Input: testInput{Name: "Kamil"}})
	if err != nil {
		t.Fatalf("marshal binary request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/neo/user.create", bytes.NewReader(raw))
	req.Header.Set("Content-Type", BinaryContentType)

	input, err := readInput(req)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}

	decoded, err := decodeInput[testInput](input)
	if err != nil {
		t.Fatalf("decode typed input: %v", err)
	}
	if decoded.Name != "Kamil" {
		t.Fatalf("name = %q, want Kamil", decoded.Name)
	}
}

func TestClientTypedCallUsesBinaryCodec(t *testing.T) {
	router := NewRouter()
	router.Register("hello", Query(func(_ context.Context, in testInput) (testOutput, error) {
		if in.Name != "Neo" {
			t.Fatalf("input name = %q, want Neo", in.Name)
		}
		return testOutput{Message: "hi " + in.Name}, nil
	}))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("method = %q, want POST for binary query input", got)
		}
		if got := r.Header.Get("Content-Type"); got != BinaryContentType {
			t.Fatalf("content type = %q, want %s", got, BinaryContentType)
		}
		if got := r.Header.Get("Accept"); got != BinaryContentType {
			t.Fatalf("accept = %q, want %s", got, BinaryContentType)
		}
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL+"/neo", WithBinaryCodec())
	got, err := CallTyped[testInput, testOutput](
		context.Background(),
		client.Query.Procedure("hello"),
		testInput{Name: "Neo"},
	)
	if err != nil {
		t.Fatalf("call typed: %v", err)
	}
	if got.Message != "hi Neo" {
		t.Fatalf("message = %q, want hi Neo", got.Message)
	}
}

func TestClientBinaryCallReadsJSONErrorResponse(t *testing.T) {
	router := NewRouter()
	router.Register("fail", Query(func(context.Context, struct{}) (string, error) {
		return "", NewError(CodeBadRequest, "bad input")
	}))

	server := newTestServer(router)
	defer server.Close()

	client := NewClient(server.URL+"/neo", WithBinaryCodec())
	_, err := CallTyped[struct{}, string](context.Background(), client.Query.Procedure("fail"), struct{}{})
	if err == nil {
		t.Fatal("error = nil, want bad input")
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %T, want *Error", err)
	}
	if rpcErr.Code != CodeBadRequest || rpcErr.Message != "bad input" {
		t.Fatalf("error = %#v, want BAD_REQUEST bad input", rpcErr)
	}
}
