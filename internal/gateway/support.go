package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	jsoncodec "github.com/Protocol-Lattice/neo/internal/codec/json"
	neoerrors "github.com/Protocol-Lattice/neo/internal/errors"
	routerruntime "github.com/Protocol-Lattice/neo/internal/router"
)

const MetadataPath = routerruntime.MetadataPath

const (
	CodeMethodNotAllowed = neoerrors.CodeMethodNotAllowed
	CodeUnauthorized     = neoerrors.CodeUnauthorized
	CodeNotFound         = neoerrors.CodeNotFound
	CodeInternal         = neoerrors.CodeInternal
	CodeUnavailable      = neoerrors.CodeUnavailable
)

var (
	NewError  = neoerrors.NewError
	Errorf    = neoerrors.Errorf
	WrapError = neoerrors.WrapError
)

type Response = jsoncodec.Response
type CORSOptions = routerruntime.CORSOptions
type ProcedureMeta = routerruntime.ProcedureMeta
type Router = routerruntime.Router
type ServerOptions = routerruntime.ServerOptions

func serverOptionsWithDefaults(opts routerruntime.ServerOptions) routerruntime.ServerOptions {
	if opts.Addr == "" {
		opts.Addr = routerruntime.DefaultAddr
	}
	if opts.Prefix == "" {
		opts.Prefix = routerruntime.DefaultPrefix
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
	if opts.MaxRequestBody <= 0 {
		opts.MaxRequestBody = routerruntime.DefaultMaxRequestBody
	}
	return opts
}

func writeCORSHeaders(w http.ResponseWriter, r *http.Request, opts routerruntime.CORSOptions) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}

	allowedOrigin, ok := corsAllowedOrigin(origin, opts)
	if !ok {
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
	w.Header().Set("Vary", "Origin")
	allowedMethods := corsValues(opts.AllowedMethods, []string{"GET", "HEAD", "POST", "OPTIONS"})
	allowedHeaders := corsValues(opts.AllowedHeaders, []string{"Content-Type", "Authorization", "Accept"})
	w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowedMethods, ", "))
	w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
	if opts.AllowCredentials {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	if opts.MaxAge > 0 {
		w.Header().Set("Access-Control-Max-Age", strings.TrimSuffix(opts.MaxAge.Truncate(time.Second).String(), "s"))
	}
}

func corsAllowedOrigin(origin string, opts routerruntime.CORSOptions) (string, bool) {
	if len(opts.AllowedOrigins) == 0 {
		return origin, true
	}
	for _, allowed := range opts.AllowedOrigins {
		switch allowed {
		case "*":
			if opts.AllowCredentials {
				return origin, true
			}
			return "*", true
		case origin:
			return origin, true
		}
	}
	return "", false
}

func corsValues(values []string, defaults []string) []string {
	if len(values) == 0 {
		return defaults
	}
	return values
}

func serveProcedureMetadata(w http.ResponseWriter, r *http.Request, metadata []routerruntime.ProcedureMeta) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writeProcedureError(w, NewError(CodeMethodNotAllowed, "metadata requires GET"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	_ = json.NewEncoder(w).Encode(metadata)
}

func fetchProcedureMetadata(
	ctx context.Context,
	httpClient *http.Client,
	endpoint string,
	headers http.Header,
) ([]routerruntime.ProcedureMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create metadata request: %w", err)
	}
	req.Header.Set("Accept", jsoncodec.ContentType)
	req.Header.Set("Content-Type", jsoncodec.ContentType)
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata: %w", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode >= 400 {
		var rpcRes Response
		if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
			return nil, Errorf(CodeInternal, "metadata failed with status %d", res.StatusCode)
		}
		if rpcRes.Error != "" || rpcRes.Code != "" {
			return nil, neoerrors.ResponseError(rpcRes.Code, rpcRes.Error)
		}

		return nil, Errorf(CodeInternal, "metadata failed with status %d", res.StatusCode)
	}

	var metadata []routerruntime.ProcedureMeta
	if err := json.NewDecoder(res.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}

	return metadata, nil
}

func writeProcedureError(w http.ResponseWriter, err error) {
	n := neoerrors.Normalize(err)
	e := n.Error
	message := e.Message

	if e.Code == CodeInternal {
		if neoerrors.ErrorLogger != nil {
			neoerrors.ErrorLogger.Printf("internal procedure error: %v", err)
		}
		message = neoerrors.InternalMessage()
	} else if !n.Explicit {
		message = e.Code.DefaultMessage()
	}

	jsoncodec.Write(w, e.Code.HTTPStatus(), Response{Code: string(e.Code), Error: message})
}
