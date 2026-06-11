package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	routerruntime "github.com/Protocol-Lattice/neo/internal/router"
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

type typedResponse[T any] struct {
	Result T      `json:"result,omitempty"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

const largeGETInputBytes = 6 << 10
const internalErrorMessage = "internal server error"

type (
	Handler                                = routerruntime.Handler
	Middleware                             = routerruntime.Middleware
	Procedure[Fn, In, Out any]             = routerruntime.Procedure[Fn, In, Out]
	ProcedureMeta                          = routerruntime.ProcedureMeta
	ProcedureOption                        = routerruntime.ProcedureOption
	Response                               = routerruntime.Response
	Router                                 = routerruntime.Router
	ServerOptions                          = routerruntime.ServerOptions
	SubscriptionProcedure[Fn, In, Out any] = routerruntime.SubscriptionProcedure[Fn, In, Out]
	Error                                  = routerruntime.Error
	ErrorCode                              = routerruntime.ErrorCode
	CORSOptions                            = routerruntime.CORSOptions
)

const (
	MetadataPath          = routerruntime.MetadataPath
	CodeBadRequest        = routerruntime.CodeBadRequest
	CodeUnauthorized      = routerruntime.CodeUnauthorized
	CodeForbidden         = routerruntime.CodeForbidden
	CodeNotFound          = routerruntime.CodeNotFound
	CodeMethodNotAllowed  = routerruntime.CodeMethodNotAllowed
	CodeConflict          = routerruntime.CodeConflict
	CodeInternal          = routerruntime.CodeInternal
	CodeNotImplemented    = routerruntime.CodeNotImplemented
	CodeUnavailable       = routerruntime.CodeUnavailable
	CodeTimeout           = routerruntime.CodeTimeout
	ProcedureKindQuery    = routerruntime.ProcedureKindQuery
	ProcedureKindMutation = routerruntime.ProcedureKindMutation
	DefaultMaxRequestBody = routerruntime.DefaultMaxRequestBody
)

var (
	NewRouter                 = routerruntime.NewRouter
	NewError                  = routerruntime.NewError
	Errorf                    = routerruntime.Errorf
	WrapError                 = routerruntime.WrapError
	ServerOptionsWithDefaults = routerruntime.ServerOptionsWithDefaults
)

func Query[In, Out any](
	fn func(context.Context, In) (Out, error),
	opts ...ProcedureOption,
) *Procedure[any, any, any] {
	return routerruntime.Query[In, Out](fn, opts...)
}

func Mutation[In, Out any](
	fn func(context.Context, In) (Out, error),
	opts ...ProcedureOption,
) *Procedure[any, any, any] {
	return routerruntime.Mutation[In, Out](fn, opts...)
}

func Subscription[In, Out any](
	fn func(context.Context, In) (<-chan Out, error),
	opts ...ProcedureOption,
) *SubscriptionProcedure[any, any, any] {
	return routerruntime.Subscription[In, Out](fn, opts...)
}

// newTestServer mounts a router under /neo/ and returns an httptest server.
// Existing tests can create a client with NewClient(server.URL + "/neo").
func newTestServer(router *Router) *httptest.Server {
	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")
	return httptest.NewServer(mux)
}
