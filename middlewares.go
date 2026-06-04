package neo

import "context"

type Handler func(ctx context.Context, input any) (any, error)

type Middleware func(next Handler) Handler
