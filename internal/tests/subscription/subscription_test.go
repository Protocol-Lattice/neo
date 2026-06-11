package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	clientruntime "github.com/Protocol-Lattice/neo/internal/client"
	routest "github.com/Protocol-Lattice/neo/internal/tests"
)

// TestClientSubscribeHandlesLargePayload exercises the bufio.Reader path with a
// line well over bufio.MaxScanTokenSize (64 KiB), which the old scanner-based
// implementation would silently drop.
func TestClientSubscribeHandlesLargePayload(t *testing.T) {
	const size = 200 * 1024 // 200 KiB, far past the old 64 KiB cap
	big := strings.Repeat("x", size)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(routest.Response{Result: big})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := clientruntime.NewClient(server.URL)
	stream, err := client.Subscription.Procedure("events").Subscribe(ctx, nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	select {
	case value, ok := <-stream:
		if !ok {
			t.Fatal("stream closed before delivering the large payload")
		}
		got, ok := value.(string)
		if !ok {
			t.Fatalf("stream value = %T, want string", value)
		}
		if len(got) != size {
			t.Fatalf("payload length = %d, want %d", len(got), size)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for large payload")
	}
}
