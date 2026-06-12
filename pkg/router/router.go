package router

import (
	"context"

	gatewayruntime "github.com/Protocol-Lattice/neo/internal/gateway"
	runtime "github.com/Protocol-Lattice/neo/internal/router"
)

const (
	DefaultAddr                     = runtime.DefaultAddr
	DefaultPrefix                   = runtime.DefaultPrefix
	DefaultMaxRequestBody           = runtime.DefaultMaxRequestBody
	DefaultEventBusSubscriberBuffer = runtime.DefaultEventBusSubscriberBuffer
	MetadataPath                    = runtime.MetadataPath
	GatewayHealthPath               = gatewayruntime.GatewayHealthPath
	BinaryContentType               = runtime.BinaryContentType

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
	BinaryCodec                            = runtime.BinaryCodec
	CORSOptions                            = runtime.CORSOptions
	Error                                  = runtime.Error
	ErrorCode                              = runtime.ErrorCode
	Event                                  = runtime.Event
	EventBroker                            = runtime.EventBroker
	EventBus                               = runtime.EventBus
	EventBusOptions                        = runtime.EventBusOptions
	Gateway                                = gatewayruntime.Gateway
	GatewayDiagnostics                     = gatewayruntime.GatewayDiagnostics
	GatewayServiceDiagnostics              = gatewayruntime.GatewayServiceDiagnostics
	Handler                                = runtime.Handler
	Middleware                             = runtime.Middleware
	Observation                            = runtime.Observation
	Observer                               = runtime.Observer
	Procedure[Fn, In, Out any]             = runtime.Procedure[Fn, In, Out]
	ProcedureKind                          = runtime.ProcedureKind
	ProcedureMeta                          = runtime.ProcedureMeta
	ProcedureOption                        = runtime.ProcedureOption
	ProxyOption                            = gatewayruntime.ProxyOption
	Router                                 = runtime.Router
	ServerOptions                          = runtime.ServerOptions
	SubscriptionProcedure[Fn, In, Out any] = runtime.SubscriptionProcedure[Fn, In, Out]
	Validator                              = runtime.Validator
)

var (
	ErrorLogger    = runtime.ErrorLogger
	NeoBinaryCodec = runtime.NeoBinaryCodec

	NewRouter              = runtime.NewRouter
	NewEventBus            = runtime.NewEventBus
	NewEventBusWithOptions = runtime.NewEventBusWithOptions

	NewGateway                 = gatewayruntime.NewGateway
	WithProxyTransport         = gatewayruntime.WithProxyTransport
	WithProxyHeader            = gatewayruntime.WithProxyHeader
	WithProxyHeaders           = gatewayruntime.WithProxyHeaders
	WithProxyMetadata          = gatewayruntime.WithProxyMetadata
	WithProxyMetadataDiscovery = gatewayruntime.WithProxyMetadataDiscovery
	WithProxyMetadataTimeout   = gatewayruntime.WithProxyMetadataTimeout

	NewError          = runtime.NewError
	Errorf            = runtime.Errorf
	Observe           = runtime.Observe
	Recover           = runtime.Recover
	WrapError         = runtime.WrapError
	WithSummary       = runtime.WithSummary
	WithDescription   = runtime.WithDescription
	WithTags          = runtime.WithTags
	WithDeprecated    = runtime.WithDeprecated
	WithProcedureMeta = runtime.WithProcedureMeta
)

func New() *Router {
	return runtime.NewRouter()
}

func Query[In, Out any](
	fn func(context.Context, In) (Out, error),
	opts ...ProcedureOption,
) *Procedure[any, any, any] {
	return runtime.Query[In, Out](fn, opts...)
}

func Mutation[In, Out any](
	fn func(context.Context, In) (Out, error),
	opts ...ProcedureOption,
) *Procedure[any, any, any] {
	return runtime.Mutation[In, Out](fn, opts...)
}

func Subscription[In, Out any](
	fn func(context.Context, In) (<-chan Out, error),
	opts ...ProcedureOption,
) *SubscriptionProcedure[any, any, any] {
	return runtime.Subscription[In, Out](fn, opts...)
}
