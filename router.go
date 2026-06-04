package neo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultAddr           = ":8080"
	DefaultPrefix         = "/neo/"
	DefaultMaxRequestBody = 1 << 20 // 1 MiB
)

// Router maps procedure keys to handlers and serves them over HTTP.
//
// Concurrency contract: a Router is NOT safe for concurrent registration and
// serving. All registration (Register, RegisterSubscription, Use, Merge,
// Nested) must complete before the first request is served; the internal maps
// are deliberately unsynchronized for zero per-request locking. Once you call
// Serve or hand the router to net/http, treat it as read-only.
//
// Middleware contract: middleware is snapshotted at Register/RegisterSubscription
// time, so Use must be called BEFORE the procedures it should wrap. Calling Use
// after a procedure is registered does not retroactively apply to it. For scoped
// middleware, build a sub-router, call Use on it, register into it, then attach
// it with Nested or Merge.
type Router struct {
	procedures              map[string]*Procedure[any, any, any]
	subscriptions           map[string]*SubscriptionProcedure[any, any, any]
	procedureMiddlewares    map[string][]Middleware
	subscriptionMiddlewares map[string][]Middleware
	middlewares             []Middleware
	events                  EventBroker
	metadata                map[string]ProcedureMeta
}

type ServerOptions struct {
	Addr           string
	Prefix         string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	MaxRequestBody int64
}

func NewRouter() *Router {
	return &Router{
		procedures:              make(map[string]*Procedure[any, any, any]),
		subscriptions:           make(map[string]*SubscriptionProcedure[any, any, any]),
		procedureMiddlewares:    make(map[string][]Middleware),
		subscriptionMiddlewares: make(map[string][]Middleware),
		events:                  NewEventBus(),
		metadata:                make(map[string]ProcedureMeta),
	}
}

func (opts ServerOptions) withDefaults() ServerOptions {
	if opts.Addr == "" {
		opts.Addr = DefaultAddr
	}
	if opts.Prefix == "" {
		opts.Prefix = DefaultPrefix
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = 5 * time.Second
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 10 * time.Second
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 60 * time.Second
	}
	if opts.MaxRequestBody == 0 {
		opts.MaxRequestBody = DefaultMaxRequestBody
	}
	return opts
}

// Use appends global middleware. It must be called before the procedures it
// should wrap: middleware is snapshotted into each procedure at registration
// time and is not applied retroactively. See the Router doc comment.
func (router *Router) Use(middlewares ...Middleware) {
	router.ensure()
	router.middlewares = append(router.middlewares, middlewares...)
}

func (router *Router) Events() EventBroker {
	router.ensure()
	return router.events
}

// UseEvents replaces the default in-memory EventBus with a custom broker.
//
// Use this to plug in Redis, NATS, Kafka, RabbitMQ, Postgres LISTEN/NOTIFY,
// or another distributed pub/sub implementation. Passing nil restores the
// default in-memory EventBus. Call UseEvents during setup, before serving.
func (router *Router) UseEvents(events EventBroker) {
	router.ensure()
	if events == nil {
		router.events = NewEventBus()
		return
	}
	router.events = events
}

func (router *Router) Metadata() []ProcedureMeta {
	router.ensure()

	metas := make([]ProcedureMeta, 0, len(router.metadata))
	for _, meta := range router.metadata {
		metas = append(metas, meta)
	}

	return metas
}

func (router *Router) Register(key string, procedure *Procedure[any, any, any]) {
	router.ensure()
	key = strings.Trim(key, ".")
	router.procedures[key] = procedure
	router.procedureMiddlewares[key] = cloneMiddlewares(router.middlewares)

	if procedure != nil {
		meta := procedure.Meta
		meta.Key = key
		if meta.Kind == "" {
			meta.Kind = procedure.Kind
		}
		if meta.Kind == "" {
			meta.Kind = ProcedureKindQuery
		}
		router.metadata[key] = meta
	}
}

func (router *Router) RegisterSubscription(key string, procedure *SubscriptionProcedure[any, any, any]) {
	router.ensure()
	key = strings.Trim(key, ".")
	router.subscriptions[key] = procedure
	router.subscriptionMiddlewares[key] = cloneMiddlewares(router.middlewares)

	if procedure != nil {
		meta := procedure.Meta
		meta.Key = key
		if meta.Kind == "" {
			meta.Kind = ProcedureKindSubscription
		}
		router.metadata[key] = meta
	}
}

func (router *Router) Method(key string) *Procedure[any, any, any] {
	if router == nil {
		return nil
	}

	return router.procedures[strings.Trim(key, ".")]
}

func (router *Router) Subscription(key string) *SubscriptionProcedure[any, any, any] {
	if router == nil {
		return nil
	}

	return router.subscriptions[strings.Trim(key, ".")]
}

func (router *Router) Merge(other *Router) {
	router.ensure()

	if other == nil {
		return
	}

	for key, procedure := range other.procedures {
		router.procedures[key] = procedure
		router.procedureMiddlewares[key] = appendMiddlewares(router.middlewares, other.procedureMiddlewares[key])
	}
	for key, procedure := range other.subscriptions {
		router.subscriptions[key] = procedure
		router.subscriptionMiddlewares[key] = appendMiddlewares(router.middlewares, other.subscriptionMiddlewares[key])
	}
	for key, meta := range other.metadata {
		router.metadata[key] = meta
	}
}

func (router *Router) Nested(prefix string, nested *Router) {
	router.ensure()

	if nested == nil {
		return
	}

	prefix = strings.Trim(prefix, ".")
	if prefix == "" {
		router.Merge(nested)
		return
	}

	for key, procedure := range nested.procedures {
		key = strings.Trim(key, ".")
		fullKey := prefix + "." + key
		router.procedures[fullKey] = procedure
		router.procedureMiddlewares[fullKey] = appendMiddlewares(router.middlewares, nested.procedureMiddlewares[key])
	}
	for key, procedure := range nested.subscriptions {
		key = strings.Trim(key, ".")
		fullKey := prefix + "." + key
		router.subscriptions[fullKey] = procedure
		router.subscriptionMiddlewares[fullKey] = appendMiddlewares(router.middlewares, nested.subscriptionMiddlewares[key])
	}
	for key, meta := range nested.metadata {
		key = strings.Trim(key, ".")
		meta.Key = prefix + "." + key
		router.metadata[meta.Key] = meta
	}
}

// Serve starts a hardened HTTP server on :8080 using the default /neo/ prefix.
func (router *Router) Serve() error {
	return router.ListenAndServe(ServerOptions{})
}

// ListenAndServe starts a hardened HTTP server with configurable address,
// prefix, timeouts, and maximum request-body size.
func (router *Router) ListenAndServe(opts ServerOptions) error {
	opts = opts.withDefaults()

	mux := http.NewServeMux()
	router.ServeHTTP(mux, opts.Prefix)

	server := &http.Server{
		Addr:         opts.Addr,
		Handler:      http.MaxBytesHandler(mux, opts.MaxRequestBody),
		ReadTimeout:  opts.ReadTimeout,
		WriteTimeout: opts.WriteTimeout,
		IdleTimeout:  opts.IdleTimeout,
	}

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (router *Router) ServeHTTP(mux *http.ServeMux, prefix string) {
	router.ensure()

	prefix = "/" + strings.Trim(prefix, "/") + "/"

	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		writeCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.Header().Set("Allow", "GET, HEAD, POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		key := strings.TrimPrefix(r.URL.Path, prefix)
		key = strings.Trim(key, "/")

		if subscription := router.Subscription(key); subscription != nil {
			router.serveSubscription(w, r, key, subscription)
			return
		}

		procedure := router.Method(key)
		if procedure == nil {
			writeProcedureError(w, NewError(CodeNotFound, "procedure not found"))
			return
		}

		if want, enforced := expectedMethod(procedure.Kind); enforced && !methodMatches(r.Method, want) {
			w.Header().Set("Allow", allowedMethods(want))
			writeProcedureError(w, Errorf(CodeMethodNotAllowed, "%s requires %s", procedure.Kind, want))
			return
		}

		input, err := readInput(r)
		if err != nil {
			writeProcedureError(w, WrapError(CodeBadRequest, err.Error(), err))
			return
		}

		handler := applyMiddlewares(router.procedureMiddlewares[key], func(ctx context.Context, input any) (any, error) {
			return procedure.Call(ctx, procedure.Fn, input)
		})

		output, err := handler(r.Context(), input)
		if err != nil {
			writeProcedureError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, Response{
			Result: output,
		})
	})
}

func (router *Router) serveSubscription(w http.ResponseWriter, r *http.Request, key string, subscription *SubscriptionProcedure[any, any, any]) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, OPTIONS")
		writeProcedureError(w, NewError(CodeMethodNotAllowed, "subscriptions require GET"))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProcedureError(w, NewError(CodeInternal, "streaming is not supported"))
		return
	}

	input, err := readInput(r)
	if err != nil {
		writeProcedureError(w, WrapError(CodeBadRequest, err.Error(), err))
		return
	}

	handler := applyMiddlewares(router.subscriptionMiddlewares[key], func(ctx context.Context, input any) (any, error) {
		return subscription.Call(ctx, subscription.Fn, input)
	})

	rawStream, err := handler(r.Context(), input)
	if err != nil {
		writeProcedureError(w, err)
		return
	}

	stream, ok := rawStream.(<-chan any)
	if !ok {
		writeProcedureError(w, NewError(CodeInternal, "invalid subscription stream"))
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	encoder := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case value, ok := <-stream:
			if !ok {
				return
			}

			if err := encoder.Encode(Response{Result: value}); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func applyMiddlewares(middlewares []Middleware, handler Handler) Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}

	return handler
}

// expectedMethod reports the HTTP method a procedure kind is served over.
// Queries are GET, mutations are POST; subscriptions are handled separately
// (and also require GET). The bool is false for kinds that are not enforced.
func expectedMethod(kind ProcedureKind) (string, bool) {
	switch kind {
	case ProcedureKindQuery:
		return http.MethodGet, true
	case ProcedureKindMutation:
		return http.MethodPost, true
	default:
		return "", false
	}
}

func methodMatches(got, want string) bool {
	if got == want {
		return true
	}
	if want == http.MethodGet {
		return got == http.MethodHead || got == http.MethodPost
	}
	return false
}

func allowedMethods(want string) string {
	if want == http.MethodGet {
		return "GET, HEAD, POST, OPTIONS"
	}
	return want + ", OPTIONS"
}

func writeCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept")
}

func cloneMiddlewares(middlewares []Middleware) []Middleware {
	return append([]Middleware(nil), middlewares...)
}

func appendMiddlewares(first []Middleware, second []Middleware) []Middleware {
	out := make([]Middleware, 0, len(first)+len(second))
	out = append(out, first...)
	out = append(out, second...)
	return out
}

func (router *Router) ensure() {
	if router.procedures == nil {
		router.procedures = make(map[string]*Procedure[any, any, any])
	}
	if router.subscriptions == nil {
		router.subscriptions = make(map[string]*SubscriptionProcedure[any, any, any])
	}
	if router.procedureMiddlewares == nil {
		router.procedureMiddlewares = make(map[string][]Middleware)
	}
	if router.subscriptionMiddlewares == nil {
		router.subscriptionMiddlewares = make(map[string][]Middleware)
	}
	if router.events == nil {
		router.events = NewEventBus()
	}
	if router.metadata == nil {
		router.metadata = make(map[string]ProcedureMeta)
	}
}
