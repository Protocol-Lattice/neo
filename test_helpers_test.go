package neo

import (
	"net/http"
	"net/http/httptest"
)

// testInput and testOutput are shared fixtures used by the package tests.
// Keep them intentionally small and JSON-friendly so they exercise the
// framework's any -> JSON -> typed value path without depending on production
// types.
type testInput struct {
	Name    string `json:"name,omitempty"`
	Message string `json:"message,omitempty"`
	ID      int    `json:"id,omitempty"`
	Value   int    `json:"value,omitempty"`
}

type testOutput struct {
	Message string `json:"message,omitempty"`
	Name    string `json:"name,omitempty"`
	ID      int    `json:"id,omitempty"`
	Value   int    `json:"value,omitempty"`
}

// newTestServer mounts a router under /neo/ and returns an httptest server.
// Existing tests can create a client with NewClient(server.URL + "/neo").
func newTestServer(router *Router) *httptest.Server {
	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")
	return httptest.NewServer(mux)
}
