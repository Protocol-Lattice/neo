package client

import (
	"net/http"
	"net/http/httptest"

	routerruntime "github.com/Protocol-Lattice/neo/internal/router"
)

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

func newTestServer(router *routerruntime.Router) *httptest.Server {
	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")
	return httptest.NewServer(mux)
}
