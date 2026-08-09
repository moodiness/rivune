package requestwork

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// QueryTracer records every PostgreSQL query, including failed queries, against
// the request-local counters carried by ctx. It does not retain SQL or arguments.
type QueryTracer struct {
	Now func() int64
}

func (tracer QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	if counters := FromContext(ctx); counters != nil {
		counters.beginDB(tracer.now())
	}
	return ctx
}

func (tracer QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if counters := FromContext(ctx); counters != nil {
		counters.endDB(tracer.now())
	}
}

func (tracer QueryTracer) now() int64 {
	if tracer.Now != nil {
		return tracer.Now()
	}
	return Now()
}
