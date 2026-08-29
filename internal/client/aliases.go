package client

import (
	"context"

	binarycodec "github.com/Protocol-Lattice/neo/internal/codec/binary"
	neoerrors "github.com/Protocol-Lattice/neo/internal/errors"
	"github.com/Protocol-Lattice/neo/internal/procedure"
)

const (
	BinaryContentType = binarycodec.ContentType
	MetadataPath      = "_meta"
	BatchPath         = "_batch"

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
)

type (
	BinaryCodec = binarycodec.Codec

	Error         = neoerrors.Error
	ErrorCode     = neoerrors.ErrorCode
	ProcedureKind = procedure.ProcedureKind
	ProcedureMeta = procedure.ProcedureMeta
)

var (
	NeoBinaryCodec = binarycodec.Default
	NewError       = neoerrors.NewError
	Errorf         = neoerrors.Errorf
	WrapError      = neoerrors.WrapError
)

func ensureContext(ctx context.Context) context.Context {
	return procedure.EnsureContext(ctx)
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

func responseError(res Response) error {
	return neoerrors.ResponseError(res.Code, res.Error)
}
