package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	routest "github.com/Protocol-Lattice/neo/internal/tests"
)

func TestMetadataReturnsStableKeyOrder(t *testing.T) {
	router := routest.NewRouter()
	router.Register("zeta", routest.Query(func(context.Context, struct{}) (string, error) { return "", nil }))
	router.Register("alpha", routest.Query(func(context.Context, struct{}) (string, error) { return "", nil }))
	router.RegisterSubscription("events", routest.Subscription(func(context.Context, struct{}) (<-chan string, error) {
		ch := make(chan string)
		close(ch)
		return ch, nil
	}))

	metas := router.Metadata()
	got := make([]string, 0, len(metas))
	for _, meta := range metas {
		got = append(got, meta.Key)
	}

	want := []string{"alpha", "events", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("metadata keys = %#v, want %#v", got, want)
	}
}

func TestMetadataEndpointReturnsProcedureMetadata(t *testing.T) {
	router := routest.NewRouter()
	router.Register("zeta", routest.Query(func(context.Context, struct{}) (string, error) { return "", nil }))
	router.Register("alpha", routest.Mutation(func(context.Context, routest.Input) (routest.Output, error) {
		return routest.Output{}, nil
	}))
	router.RegisterSubscription("events", routest.Subscription(func(context.Context, struct{}) (<-chan string, error) {
		ch := make(chan string)
		close(ch)
		return ch, nil
	}))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/neo/"+routest.MetadataPath, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var metadata []routest.ProcedureMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if len(metadata) != 3 {
		t.Fatalf("metadata len = %d, want 3", len(metadata))
	}

	got := []string{metadata[0].Key, metadata[1].Key, metadata[2].Key}
	want := []string{"alpha", "events", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("metadata keys = %#v, want %#v", got, want)
	}
	if metadata[0].Kind != routest.ProcedureKindMutation {
		t.Fatalf("alpha kind = %q, want mutation", metadata[0].Kind)
	}
}

func TestMetadataEndpointRejectsUnsupportedMethods(t *testing.T) {
	router := routest.NewRouter()
	router.Register("ping", routest.Query(func(context.Context, struct{}) (string, error) { return "pong", nil }))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/neo/"+routest.MetadataPath, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD, OPTIONS" {
		t.Fatalf("allow = %q, want GET, HEAD, OPTIONS", got)
	}
}

func TestNilRegistrationRemovesMetadata(t *testing.T) {
	router := routest.NewRouter()
	router.Register("ping", routest.Query(func(context.Context, struct{}) (string, error) {
		return "pong", nil
	}))
	router.Register("ping", nil)

	if router.Method("ping") != nil {
		t.Fatal("method = non-nil, want nil")
	}
	if got := router.Metadata(); len(got) != 0 {
		t.Fatalf("metadata = %#v, want empty", got)
	}

	router.RegisterSubscription("events", routest.Subscription(func(context.Context, struct{}) (<-chan string, error) {
		ch := make(chan string)
		close(ch)
		return ch, nil
	}))
	router.RegisterSubscription("events", nil)

	if router.Subscription("events") != nil {
		t.Fatal("subscription = non-nil, want nil")
	}
	if got := router.Metadata(); len(got) != 0 {
		t.Fatalf("metadata = %#v, want empty", got)
	}
}
