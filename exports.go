package neo

import (
	"context"

	clientpkg "github.com/Protocol-Lattice/neo/pkg/client"
	routerpkg "github.com/Protocol-Lattice/neo/pkg/router"
)

const (
	DefaultAddr                     = routerpkg.DefaultAddr
	DefaultPrefix                   = routerpkg.DefaultPrefix
	DefaultMaxRequestBody           = routerpkg.DefaultMaxRequestBody
	DefaultEventBusSubscriberBuffer = routerpkg.DefaultEventBusSubscriberBuffer
	MetadataPath                    = routerpkg.MetadataPath
	GatewayHealthPath               = routerpkg.GatewayHealthPath
	BinaryContentType               = routerpkg.BinaryContentType

	CodeBadRequest       = routerpkg.CodeBadRequest
	CodeUnauthorized     = routerpkg.CodeUnauthorized
	CodeForbidden        = routerpkg.CodeForbidden
	CodeNotFound         = routerpkg.CodeNotFound
	CodeMethodNotAllowed = routerpkg.CodeMethodNotAllowed
	CodeConflict         = routerpkg.CodeConflict
	CodeTooManyRequests  = routerpkg.CodeTooManyRequests
	CodeInternal         = routerpkg.CodeInternal
	CodeNotImplemented   = routerpkg.CodeNotImplemented
	CodeUnavailable      = routerpkg.CodeUnavailable
	CodeTimeout          = routerpkg.CodeTimeout

	ProcedureKindQuery        = routerpkg.ProcedureKindQuery
	ProcedureKindMutation     = routerpkg.ProcedureKindMutation
	ProcedureKindSubscription = routerpkg.ProcedureKindSubscription
)

type (
	BinaryCodec = routerpkg.BinaryCodec

	Client          = clientpkg.Client
	ClientNamespace = clientpkg.ClientNamespace
	ClientOption    = clientpkg.ClientOption
	ClientProcedure = clientpkg.ClientProcedure
	Request         = clientpkg.Request
	Response        = clientpkg.Response

	CORSOptions                            = routerpkg.CORSOptions
	Event                                  = routerpkg.Event
	EventBroker                            = routerpkg.EventBroker
	EventBus                               = routerpkg.EventBus
	EventBusOptions                        = routerpkg.EventBusOptions
	Gateway                                = routerpkg.Gateway
	GatewayDiagnostics                     = routerpkg.GatewayDiagnostics
	GatewayServiceDiagnostics              = routerpkg.GatewayServiceDiagnostics
	Handler                                = routerpkg.Handler
	Middleware                             = routerpkg.Middleware
	Procedure[Fn, In, Out any]             = routerpkg.Procedure[Fn, In, Out]
	ProcedureKind                          = routerpkg.ProcedureKind
	ProcedureMeta                          = routerpkg.ProcedureMeta
	ProcedureOption                        = routerpkg.ProcedureOption
	ProxyOption                            = routerpkg.ProxyOption
	Router                                 = routerpkg.Router
	ServerOptions                          = routerpkg.ServerOptions
	SubscriptionProcedure[Fn, In, Out any] = routerpkg.SubscriptionProcedure[Fn, In, Out]
	Validator                              = routerpkg.Validator

	Error     = routerpkg.Error
	ErrorCode = routerpkg.ErrorCode
)

var (
	ErrorLogger    = routerpkg.ErrorLogger
	NeoBinaryCodec = routerpkg.NeoBinaryCodec

	NewClient       = clientpkg.NewClient
	WithHTTPClient  = clientpkg.WithHTTPClient
	WithHeader      = clientpkg.WithHeader
	WithHeaders     = clientpkg.WithHeaders
	WithBinaryCodec = clientpkg.WithBinaryCodec

	NewRouter              = routerpkg.NewRouter
	NewEventBus            = routerpkg.NewEventBus
	NewEventBusWithOptions = routerpkg.NewEventBusWithOptions

	NewGateway                 = routerpkg.NewGateway
	WithProxyTransport         = routerpkg.WithProxyTransport
	WithProxyHeader            = routerpkg.WithProxyHeader
	WithProxyHeaders           = routerpkg.WithProxyHeaders
	WithProxyMetadata          = routerpkg.WithProxyMetadata
	WithProxyMetadataDiscovery = routerpkg.WithProxyMetadataDiscovery
	WithProxyMetadataTimeout   = routerpkg.WithProxyMetadataTimeout

	NewError          = routerpkg.NewError
	Errorf            = routerpkg.Errorf
	WrapError         = routerpkg.WrapError
	WithSummary       = routerpkg.WithSummary
	WithDescription   = routerpkg.WithDescription
	WithTags          = routerpkg.WithTags
	WithDeprecated    = routerpkg.WithDeprecated
	WithProcedureMeta = routerpkg.WithProcedureMeta
)

func Query[In, Out any](
	fn func(context.Context, In) (Out, error),
	opts ...ProcedureOption,
) *Procedure[any, any, any] {
	return routerpkg.Query[In, Out](fn, opts...)
}

func Mutation[In, Out any](
	fn func(context.Context, In) (Out, error),
	opts ...ProcedureOption,
) *Procedure[any, any, any] {
	return routerpkg.Mutation[In, Out](fn, opts...)
}

func Subscription[In, Out any](
	fn func(context.Context, In) (<-chan Out, error),
	opts ...ProcedureOption,
) *SubscriptionProcedure[any, any, any] {
	return routerpkg.Subscription[In, Out](fn, opts...)
}

func CallTyped[In, Out any](
	ctx context.Context,
	procedure *ClientProcedure,
	input In,
) (Out, error) {
	return clientpkg.CallTyped[In, Out](ctx, procedure, input)
}

func SubscribeTyped[In, Out any](
	ctx context.Context,
	procedure *ClientProcedure,
	input In,
) (<-chan Out, error) {
	return clientpkg.SubscribeTyped[In, Out](ctx, procedure, input)
}

func SubscribeWebSocketTyped[In, Out any](
	ctx context.Context,
	procedure *ClientProcedure,
	input In,
) (<-chan Out, error) {
	return clientpkg.SubscribeWebSocketTyped[In, Out](ctx, procedure, input)
}
