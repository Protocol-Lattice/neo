package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"time"

	routerruntime "github.com/Protocol-Lattice/neo/internal/router"
)

// GatewayHealthPath is the reserved path under a gateway HTTP prefix that
// exposes service diagnostics as JSON. With the default prefix, the endpoint is
// /neo/_health.
const GatewayHealthPath = "_health"

// Gateway exposes several Neo services behind a single HTTP prefix.
//
// Mount local routers for tests, development, or a modular monolith. Proxy
// remote service URLs for independently deployed microservices. A service named
// "users" with a procedure named "getByID" is exposed as "users.getByID" at the
// gateway, while the backing service still owns "getByID" locally.
type Gateway struct {
	services map[string]gatewayService
	cors     routerruntime.CORSOptions
}

type gatewayService struct {
	prefix   string
	router   *routerruntime.Router
	proxy    *httputil.ReverseProxy
	metadata []routerruntime.ProcedureMeta
}

// ProxyOption customizes gateway reverse proxy behavior for a remote service.
type ProxyOption func(*proxyOptions)

type proxyOptions struct {
	transport        http.RoundTripper
	headers          http.Header
	metadata         []ProcedureMeta
	discoverMetadata bool
	metadataTimeout  time.Duration
}

// GatewayDiagnostics describes the services configured behind a gateway.
type GatewayDiagnostics struct {
	Services []GatewayServiceDiagnostics `json:"services"`
}

// GatewayServiceDiagnostics describes one mounted or proxied service.
type GatewayServiceDiagnostics struct {
	Prefix         string                        `json:"prefix"`
	Mode           string                        `json:"mode"`
	ProcedureCount int                           `json:"procedureCount"`
	Procedures     []routerruntime.ProcedureMeta `json:"procedures,omitempty"`
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

// WithProxyMetadataDiscovery fetches procedure metadata from the upstream
// service's reserved metadata endpoint when Proxy is called.
//
// Headers configured with WithProxyHeader or WithProxyHeaders are sent with the
// metadata request, which lets gateways use the same service-auth headers for
// diagnostics and code generation metadata as they use for proxied calls.
func WithProxyMetadataDiscovery() ProxyOption {
	return func(opts *proxyOptions) {
		opts.discoverMetadata = true
	}
}

// WithProxyMetadataTimeout sets the metadata discovery request timeout.
// Non-positive durations are ignored. The default is 5 seconds.
func WithProxyMetadataTimeout(timeout time.Duration) ProxyOption {
	return func(opts *proxyOptions) {
		if timeout > 0 {
			opts.metadataTimeout = timeout
		}
	}
}

// WithProxyMetadata adds metadata for procedures hosted by a remote service.
//
// The gateway prefixes these keys with the service name when Metadata is read.
// Local service metadata is discovered from the mounted router automatically.
func WithProxyMetadata(metadata ...routerruntime.ProcedureMeta) ProxyOption {
	return func(opts *proxyOptions) {
		opts.metadata = append(opts.metadata, metadata...)
	}
}

// Mount exposes a local router as a named service.
func (gateway *Gateway) Mount(prefix string, router *routerruntime.Router) error {
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
	if proxyOpts.metadataTimeout == 0 {
		proxyOpts.metadataTimeout = 5 * time.Second
	}
	metadata := slices.Clone(proxyOpts.metadata)
	if proxyOpts.discoverMetadata {
		discovered, err := discoverProxyMetadata(targetURL, proxyOpts)
		if err != nil {
			return fmt.Errorf("discover proxy metadata for %q: %w", prefix, err)
		}
		metadata = append(metadata, discovered...)
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
		metadata: metadata,
	}
	return nil
}

// UseCORS configures CORS headers emitted by the gateway.
func (gateway *Gateway) UseCORS(opts routerruntime.CORSOptions) {
	gateway.ensure()
	gateway.cors = opts
}

// Metadata returns gateway procedure metadata with service prefixes applied.
func (gateway *Gateway) Metadata() []routerruntime.ProcedureMeta {
	if gateway == nil {
		return nil
	}

	metas := make([]routerruntime.ProcedureMeta, 0)
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

// Diagnostics returns configured gateway services, their mode, and the
// procedure metadata currently known for each service.
func (gateway *Gateway) Diagnostics() GatewayDiagnostics {
	if gateway == nil {
		return GatewayDiagnostics{}
	}

	prefixes := make([]string, 0, len(gateway.services))
	for prefix := range gateway.services {
		prefixes = append(prefixes, prefix)
	}
	slices.Sort(prefixes)

	services := make([]GatewayServiceDiagnostics, 0, len(prefixes))
	for _, prefix := range prefixes {
		service := gateway.services[prefix]
		mode := "proxy"
		metadata := slices.Clone(service.metadata)
		if service.router != nil {
			mode = "local"
			metadata = service.router.Metadata()
		}
		for i := range metadata {
			metadata[i].Key = joinProcedureKey(prefix, metadata[i].Key)
		}
		slices.SortFunc(metadata, func(a, b ProcedureMeta) int {
			return cmp.Compare(a.Key, b.Key)
		})

		services = append(services, GatewayServiceDiagnostics{
			Prefix:         prefix,
			Mode:           mode,
			ProcedureCount: len(metadata),
			Procedures:     metadata,
		})
	}

	return GatewayDiagnostics{Services: services}
}

// Serve starts a hardened HTTP server on :8080 using the default /neo/ prefix.
func (gateway *Gateway) Serve() error {
	return gateway.ListenAndServe(ServerOptions{})
}

// ListenAndServe starts a hardened gateway HTTP server.
func (gateway *Gateway) ListenAndServe(opts routerruntime.ServerOptions) error {
	opts = serverOptionsWithDefaults(opts)

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

	prefix = routerruntime.NormalizeHTTPPrefix(prefix)
	mux.Handle(prefix, gateway.HTTPHandler(prefix))
}

// HTTPHandler returns an HTTP handler for the gateway mounted at prefix.
func (gateway *Gateway) HTTPHandler(prefix string) http.Handler {
	gateway.ensure()

	prefix = routerruntime.NormalizeHTTPPrefix(prefix)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCORSHeaders(w, r, gateway.cors)
		if r.Method == http.MethodOptions {
			w.Header().Set("Allow", "GET, HEAD, POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		key := gatewayRequestKey(r, prefix)
		if key == MetadataPath {
			serveProcedureMetadata(w, r, gateway.Metadata())
			return
		}
		if key == GatewayHealthPath {
			serveGatewayDiagnostics(w, r, gateway.Diagnostics())
			return
		}

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

func discoverProxyMetadata(targetURL *url.URL, opts proxyOptions) ([]routerruntime.ProcedureMeta, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.metadataTimeout)
	defer cancel()

	httpClient := &http.Client{Timeout: opts.metadataTimeout}
	if opts.transport != nil {
		httpClient.Transport = opts.transport
	}

	return fetchProcedureMetadata(ctx, httpClient, proxyMetadataEndpoint(targetURL), opts.headers)
}

func proxyMetadataEndpoint(targetURL *url.URL) string {
	if targetURL == nil {
		return "/" + MetadataPath
	}

	metadataURL := *targetURL
	metadataURL.Path = joinURLPath(metadataURL.Path, MetadataPath)
	metadataURL.RawPath = ""
	metadataURL.RawQuery = ""
	metadataURL.Fragment = ""
	return metadataURL.String()
}

func serveGatewayDiagnostics(w http.ResponseWriter, r *http.Request, diagnostics GatewayDiagnostics) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writeProcedureError(w, NewError(CodeMethodNotAllowed, "gateway diagnostics require GET"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	_ = json.NewEncoder(w).Encode(diagnostics)
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
