package neo

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
)

// Gateway exposes several Neo services behind a single HTTP prefix.
//
// Mount local routers for tests, development, or a modular monolith. Proxy
// remote service URLs for independently deployed microservices. A service named
// "users" with a procedure named "getByID" is exposed as "users.getByID" at the
// gateway, while the backing service still owns "getByID" locally.
type Gateway struct {
	services map[string]gatewayService
	cors     CORSOptions
}

type gatewayService struct {
	prefix   string
	router   *Router
	proxy    *httputil.ReverseProxy
	metadata []ProcedureMeta
}

// ProxyOption customizes gateway reverse proxy behavior for a remote service.
type ProxyOption func(*proxyOptions)

type proxyOptions struct {
	transport http.RoundTripper
	headers   http.Header
	metadata  []ProcedureMeta
}

// NewGateway creates an empty microservice gateway.
func NewGateway() *Gateway {
	return &Gateway{services: make(map[string]gatewayService)}
}

// WithProxyTransport replaces the transport used when proxying to a service.
// Nil is ignored.
func WithProxyTransport(transport http.RoundTripper) ProxyOption {
	return func(opts *proxyOptions) {
		if transport != nil {
			opts.transport = transport
		}
	}
}

// WithProxyHeader sets a header on every proxied request for a service.
// Empty header names are ignored.
func WithProxyHeader(name, value string) ProxyOption {
	return func(opts *proxyOptions) {
		if name == "" {
			return
		}
		opts.headers.Set(name, value)
	}
}

// WithProxyHeaders sets headers on every proxied request for a service.
func WithProxyHeaders(headers http.Header) ProxyOption {
	return func(opts *proxyOptions) {
		for name, values := range headers {
			for _, value := range values {
				opts.headers.Add(name, value)
			}
		}
	}
}

// WithProxyMetadata adds metadata for procedures hosted by a remote service.
//
// The gateway prefixes these keys with the service name when Metadata is read.
// Local service metadata is discovered from the mounted router automatically.
func WithProxyMetadata(metadata ...ProcedureMeta) ProxyOption {
	return func(opts *proxyOptions) {
		opts.metadata = append(opts.metadata, metadata...)
	}
}

// Mount exposes a local router as a named service.
func (gateway *Gateway) Mount(prefix string, router *Router) error {
	gateway.ensure()

	prefix, err := normalizeServicePrefix(prefix)
	if err != nil {
		return err
	}
	if router == nil {
		delete(gateway.services, prefix)
		return nil
	}

	gateway.services[prefix] = gatewayService{
		prefix: prefix,
		router: router,
	}
	return nil
}

// Proxy exposes a remote Neo service URL as a named service.
//
// target should point at the remote service's Neo base URL, for example
// "http://users.internal:8080/neo". The gateway strips the service prefix before
// forwarding, so "users.getByID" becomes "getByID" on the upstream service.
func (gateway *Gateway) Proxy(prefix string, target string, opts ...ProxyOption) error {
	gateway.ensure()

	prefix, err := normalizeServicePrefix(prefix)
	if err != nil {
		return err
	}

	targetURL, err := url.Parse(strings.TrimRight(target, "/"))
	if err != nil {
		return fmt.Errorf("parse service target: %w", err)
	}
	if targetURL.Scheme == "" || targetURL.Host == "" {
		return errors.New("service target must include scheme and host")
	}

	proxyOpts := proxyOptions{headers: make(http.Header)}
	for _, opt := range opts {
		if opt != nil {
			opt(&proxyOpts)
		}
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			subkey := gatewayRequestSubkey(req)
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			req.URL.Path = joinURLPath(targetURL.Path, subkey)
			req.URL.RawPath = ""
			req.Host = targetURL.Host

			for name, values := range proxyOpts.headers {
				req.Header.Del(name)
				for _, value := range values {
					req.Header.Add(name, value)
				}
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeProcedureError(w, WrapError(CodeUnavailable, "service unavailable", err))
		},
		FlushInterval: -1,
	}
	if proxyOpts.transport != nil {
		proxy.Transport = proxyOpts.transport
	}

	gateway.services[prefix] = gatewayService{
		prefix:   prefix,
		proxy:    proxy,
		metadata: slices.Clone(proxyOpts.metadata),
	}
	return nil
}

// UseCORS configures CORS headers emitted by the gateway.
func (gateway *Gateway) UseCORS(opts CORSOptions) {
	gateway.ensure()
	gateway.cors = opts
}

// Metadata returns gateway procedure metadata with service prefixes applied.
func (gateway *Gateway) Metadata() []ProcedureMeta {
	if gateway == nil {
		return nil
	}

	metas := make([]ProcedureMeta, 0)
	for prefix, service := range gateway.services {
		if service.router != nil {
			for _, meta := range service.router.Metadata() {
				meta.Key = joinProcedureKey(prefix, meta.Key)
				metas = append(metas, meta)
			}
			continue
		}

		for _, meta := range service.metadata {
			meta.Key = joinProcedureKey(prefix, meta.Key)
			metas = append(metas, meta)
		}
	}

	slices.SortFunc(metas, func(a, b ProcedureMeta) int {
		return cmp.Compare(a.Key, b.Key)
	})
	return metas
}

// Serve starts a hardened HTTP server on :8080 using the default /neo/ prefix.
func (gateway *Gateway) Serve() error {
	return gateway.ListenAndServe(ServerOptions{})
}

// ListenAndServe starts a hardened gateway HTTP server.
func (gateway *Gateway) ListenAndServe(opts ServerOptions) error {
	opts = opts.withDefaults()

	mux := http.NewServeMux()
	gateway.ServeHTTP(mux, opts.Prefix)

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

// ServeHTTP mounts the gateway under prefix on mux.
func (gateway *Gateway) ServeHTTP(mux *http.ServeMux, prefix string) {
	gateway.ensure()

	prefix = normalizeHTTPPrefix(prefix)
	mux.Handle(prefix, gateway.HTTPHandler(prefix))
}

// HTTPHandler returns an HTTP handler for the gateway mounted at prefix.
func (gateway *Gateway) HTTPHandler(prefix string) http.Handler {
	gateway.ensure()

	prefix = normalizeHTTPPrefix(prefix)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCORSHeaders(w, r, gateway.cors)
		if r.Method == http.MethodOptions {
			w.Header().Set("Allow", "GET, HEAD, POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		key := gatewayRequestKey(r, prefix)
		service, subkey, ok := gateway.match(key)
		if !ok {
			writeProcedureError(w, NewError(CodeNotFound, "service not found"))
			return
		}

		if service.router != nil {
			req := rewriteGatewayRequest(r, prefix, subkey)
			service.router.HTTPHandler(prefix).ServeHTTP(w, req)
			return
		}

		setGatewayRequestSubkey(r, subkey)
		service.proxy.ServeHTTP(w, r)
	})
}

func (gateway *Gateway) match(key string) (gatewayService, string, bool) {
	if gateway == nil {
		return gatewayService{}, "", false
	}

	key = strings.Trim(key, "/")
	prefixes := make([]string, 0, len(gateway.services))
	for prefix := range gateway.services {
		prefixes = append(prefixes, prefix)
	}
	slices.SortFunc(prefixes, func(a, b string) int {
		if n := cmp.Compare(len(b), len(a)); n != 0 {
			return n
		}
		return cmp.Compare(a, b)
	})

	for _, prefix := range prefixes {
		switch {
		case key == prefix:
			return gateway.services[prefix], "", true
		case strings.HasPrefix(key, prefix+"."):
			return gateway.services[prefix], key[len(prefix)+1:], true
		case strings.HasPrefix(key, prefix+"/"):
			return gateway.services[prefix], key[len(prefix)+1:], true
		}
	}

	return gatewayService{}, "", false
}

func normalizeServicePrefix(prefix string) (string, error) {
	prefix = strings.Trim(prefix, "/.")
	if prefix == "" {
		return "", errors.New("service prefix must be non-empty")
	}
	if strings.Contains(prefix, "..") {
		return "", fmt.Errorf("invalid service prefix %q", prefix)
	}
	return prefix, nil
}

func gatewayRequestKey(r *http.Request, prefix string) string {
	path := r.URL.Path
	if strings.HasPrefix(path, prefix) {
		return strings.Trim(path[len(prefix):], "/")
	}
	return strings.Trim(path, "/")
}

func rewriteGatewayRequest(r *http.Request, prefix string, subkey string) *http.Request {
	req := r.Clone(r.Context())
	req.URL = cloneURL(r.URL)
	req.URL.Path = joinURLPath(prefix, subkey)
	req.URL.RawPath = ""
	return req
}

func cloneURL(raw *url.URL) *url.URL {
	if raw == nil {
		return &url.URL{}
	}
	clone := *raw
	return &clone
}

func joinProcedureKey(prefix string, key string) string {
	prefix = strings.Trim(prefix, ".")
	key = strings.Trim(key, ".")
	if prefix == "" {
		return key
	}
	if key == "" {
		return prefix
	}
	return prefix + "." + key
}

func joinURLPath(base string, subpath string) string {
	base = strings.TrimRight(base, "/")
	subpath = strings.TrimLeft(subpath, "/")

	switch {
	case base == "" && subpath == "":
		return "/"
	case base == "":
		return "/" + subpath
	case subpath == "":
		return base
	default:
		return base + "/" + subpath
	}
}

type gatewaySubkeyContextKey struct{}

func setGatewayRequestSubkey(r *http.Request, subkey string) {
	ctx := context.WithValue(r.Context(), gatewaySubkeyContextKey{}, subkey)
	*r = *r.WithContext(ctx)
}

func gatewayRequestSubkey(r *http.Request) string {
	subkey, _ := r.Context().Value(gatewaySubkeyContextKey{}).(string)
	return subkey
}

func (gateway *Gateway) ensure() {
	if gateway.services == nil {
		gateway.services = make(map[string]gatewayService)
	}
}
