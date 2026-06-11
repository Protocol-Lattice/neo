package client

import (
	"context"

	runtime "github.com/Protocol-Lattice/neo/internal/client"
)

const (
	BinaryContentType = runtime.BinaryContentType

	CodeBadRequest       = runtime.CodeBadRequest
	CodeUnauthorized     = runtime.CodeUnauthorized
	CodeForbidden        = runtime.CodeForbidden
	CodeNotFound         = runtime.CodeNotFound
	CodeMethodNotAllowed = runtime.CodeMethodNotAllowed
	CodeConflict         = runtime.CodeConflict
	CodeTooManyRequests  = runtime.CodeTooManyRequests
	CodeInternal         = runtime.CodeInternal
	CodeNotImplemented   = runtime.CodeNotImplemented
	CodeUnavailable      = runtime.CodeUnavailable
	CodeTimeout          = runtime.CodeTimeout

	ProcedureKindQuery        = runtime.ProcedureKindQuery
	ProcedureKindMutation     = runtime.ProcedureKindMutation
	ProcedureKindSubscription = runtime.ProcedureKindSubscription
)

type (
	Client          = runtime.Client
	ClientNamespace = runtime.ClientNamespace
	ClientOption    = runtime.ClientOption
	ClientProcedure = runtime.ClientProcedure
	Error           = runtime.Error
	ErrorCode       = runtime.ErrorCode
	ProcedureKind   = runtime.ProcedureKind
	ProcedureMeta   = runtime.ProcedureMeta
	Request         = runtime.Request
	Response        = runtime.Response
)

var (
	NewClient       = runtime.NewClient
	WithHTTPClient  = runtime.WithHTTPClient
	WithHeader      = runtime.WithHeader
	WithHeaders     = runtime.WithHeaders
	WithBinaryCodec = runtime.WithBinaryCodec
)

func New(addr string, opts ...ClientOption) *Client {
	return runtime.NewClient(addr, opts...)
}

func CallTyped[In, Out any](
	ctx context.Context,
	procedure *ClientProcedure,
	input In,
) (Out, error) {
	return runtime.CallTyped[In, Out](ctx, procedure, input)
}

func SubscribeTyped[In, Out any](
	ctx context.Context,
	procedure *ClientProcedure,
	input In,
) (<-chan Out, error) {
	return runtime.SubscribeTyped[In, Out](ctx, procedure, input)
}

func SubscribeWebSocketTyped[In, Out any](
	ctx context.Context,
	procedure *ClientProcedure,
	input In,
) (<-chan Out, error) {
	return runtime.SubscribeWebSocketTyped[In, Out](ctx, procedure, input)
}
