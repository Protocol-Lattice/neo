package router

import (
	"context"
	"net/http"
	"time"

	"github.com/Protocol-Lattice/neo/internal/broker"
	binarycodec "github.com/Protocol-Lattice/neo/internal/codec/binary"
	jsoncodec "github.com/Protocol-Lattice/neo/internal/codec/json"
	neoerrors "github.com/Protocol-Lattice/neo/internal/errors"
	"github.com/Protocol-Lattice/neo/internal/middleware"
	"github.com/Protocol-Lattice/neo/internal/procedure"
)

const (
	BinaryContentType = binarycodec.ContentType
	ndjsonMediaType   = jsoncodec.NDJSONContentType

	CodeBadRequest       = neoerrors.CodeBadRequest
	CodeUnauthorized     = neoerrors.CodeUnauthorized
	CodeForbidden        = neoerrors.CodeForbidden
	CodeNotFound         = neoerrors.CodeNotFound
	CodeMethodNotAllowed = neoerrors.CodeMethodNotAllowed
	CodeConflict         = neoerrors.CodeConflict
	CodeTooManyRequests  = neoerrors.CodeTooManyRequests
	CodeInternal         = neoerrors.CodeInternal
	CodeNotImplemented   = neoerrors.CodeNotImplemented
	CodeUnavailable      = neoerrors.CodeUnavailable
	CodeTimeout          = neoerrors.CodeTimeout

	ProcedureKindQuery        = procedure.ProcedureKindQuery
	ProcedureKindMutation     = procedure.ProcedureKindMutation
	ProcedureKindSubscription = procedure.ProcedureKindSubscription

	DefaultEventBusSubscriberBuffer = broker.DefaultEventBusSubscriberBuffer
)

type (
	BinaryCodec = binarycodec.Codec

	Error     = neoerrors.Error
	ErrorCode = neoerrors.ErrorCode

	Event           = broker.Event
	EventBroker     = broker.EventBroker
	EventBus        = broker.EventBus
	EventBusOptions = broker.EventBusOptions

	Handler          = middleware.Handler
	Middleware       = middleware.Middleware
	Observation      = middleware.Observation
	Observer         = middleware.Observer
	RateLimitKeyFunc = middleware.RateLimitKeyFunc
	RateLimitOption  = middleware.RateLimitOption

	Procedure[Fn, In, Out any]             = procedure.Procedure[Fn, In, Out]
	ProcedureKind                          = procedure.ProcedureKind
	ProcedureMeta                          = procedure.ProcedureMeta
	ProcedureOption                        = procedure.ProcedureOption
	SubscriptionProcedure[Fn, In, Out any] = procedure.SubscriptionProcedure[Fn, In, Out]
	Validator                              = procedure.Validator

	Request  = jsoncodec.Request
	Response = jsoncodec.Response
)

var (
	NeoBinaryCodec       = binarycodec.Default
	ErrorLogger          = neoerrors.ErrorLogger
	internalErrorMessage = neoerrors.InternalMessage()

	NewError  = neoerrors.NewError
	Errorf    = neoerrors.Errorf
	WrapError = neoerrors.WrapError

	NewEventBus            = broker.NewEventBus
	NewEventBusWithOptions = broker.NewEventBusWithOptions

	WithSummary       = procedure.WithSummary
	WithDescription   = procedure.WithDescription
	WithTags          = procedure.WithTags
	WithDeprecated    = procedure.WithDeprecated
	WithProcedureMeta = procedure.WithProcedureMeta
)

func Query[In, Out any](
	fn func(context.Context, In) (Out, error),
	opts ...ProcedureOption,
) *Procedure[any, any, any] {
	return procedure.Query[In, Out](fn, opts...)
}

func Mutation[In, Out any](
	fn func(context.Context, In) (Out, error),
	opts ...ProcedureOption,
) *Procedure[any, any, any] {
	return procedure.Mutation[In, Out](fn, opts...)
}

func Subscription[In, Out any](
	fn func(context.Context, In) (<-chan Out, error),
	opts ...ProcedureOption,
) *SubscriptionProcedure[any, any, any] {
	return procedure.Subscription[In, Out](fn, opts...)
}

func ensureContext(ctx context.Context) context.Context {
	return procedure.EnsureContext(ctx)
}

func decodeInput[T any](input any) (T, error) {
	return procedure.DecodeInput[T](input)
}

func mapStream[In, Out any](
	ctx context.Context,
	in <-chan In,
	mapValue func(In) (Out, bool),
) <-chan Out {
	return procedure.MapStream(ctx, in, mapValue)
}

func typeName[T any]() string {
	return procedure.TypeName[T]()
}

func applyMiddlewares(middlewares []Middleware, handler Handler) Handler {
	return middleware.Apply(middlewares, handler)
}

func Recover() Middleware {
	return middleware.Recover()
}

func Observe(observer Observer) Middleware {
	return middleware.Observe(observer)
}

func RateLimit(limit int, window time.Duration, opts ...RateLimitOption) Middleware {
	return middleware.RateLimit(limit, window, opts...)
}

func WithRateLimitKey(key RateLimitKeyFunc) RateLimitOption {
	return middleware.WithRateLimitKey(key)
}

func WithObservation(ctx context.Context, observation Observation) context.Context {
	return middleware.WithObservation(ctx, observation)
}

func cloneMiddlewares(middlewares []Middleware) []Middleware {
	return middleware.Clone(middlewares)
}

func appendMiddlewares(first []Middleware, second []Middleware) []Middleware {
	return middleware.Append(first, second)
}

func readInput(r *http.Request) (any, error) {
	return jsoncodec.ReadInput(r)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	jsoncodec.Write(w, status, value)
}

func writeResponse(w http.ResponseWriter, r *http.Request, status int, value any) error {
	return jsoncodec.WriteResponse(w, r, status, value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	jsoncodec.WriteError(w, status, message)
}

func writeProcedureError(w http.ResponseWriter, err error) {
	n := neoerrors.Normalize(err)
	e := n.Error
	message := e.Message

	if e.Code == CodeInternal {
		if ErrorLogger != nil {
			ErrorLogger.Printf("internal procedure error: %v", err)
		}
		message = neoerrors.InternalMessage()
	} else if !n.Explicit {
		message = e.Code.DefaultMessage()
	}

	writeJSON(w, e.Code.HTTPStatus(), Response{Code: string(e.Code), Error: message})
}

func responseError(res Response) error {
	return neoerrors.ResponseError(res.Code, res.Error)
}
