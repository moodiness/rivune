package requestwork

import (
	"context"
	"sync/atomic"
	"time"
)

var processEpoch = time.Now()

type contextKey struct{}

// Counters contains the fixed set of work measurements associated with one request.
// Its methods are safe to call from concurrent and nested operations.
type Counters struct {
	dbCalls         atomic.Int64
	dbDurationNanos atomic.Int64
	outboundCalls   atomic.Int64
	outboundNanos   atomic.Int64
	upstreamBytes   atomic.Int64
}

// Snapshot is the bounded numeric view emitted with a completed request event.
type Snapshot struct {
	DBCalls          int64
	DBDuration       time.Duration
	OutboundCalls    int64
	OutboundDuration time.Duration
	UpstreamBytes    int64
}

// WithCounters creates a request-local counter set. The context value is a single
// fixed-size pointer; operations never create labels or retain request data.
func WithCounters(ctx context.Context) (context.Context, *Counters) {
	counters := &Counters{}
	return context.WithValue(ctx, contextKey{}, counters), counters
}

func FromContext(ctx context.Context) *Counters {
	if ctx == nil {
		return nil
	}
	counters, _ := ctx.Value(contextKey{}).(*Counters)
	return counters
}

func Now() int64 {
	return time.Since(processEpoch).Nanoseconds()
}

// BeginDB and EndDB aggregate elapsed time without allocating a per-call timing
// token. Summing all end timestamps minus all start timestamps remains exact when
// calls overlap or nest.
func BeginDB(ctx context.Context, now int64) {
	if counters := FromContext(ctx); counters != nil {
		counters.beginDB(now)
	}
}

func (counters *Counters) beginDB(now int64) {
	counters.dbCalls.Add(1)
	counters.dbDurationNanos.Add(-now)
}

func EndDB(ctx context.Context, now int64) {
	if counters := FromContext(ctx); counters != nil {
		counters.endDB(now)
	}
}

func (counters *Counters) endDB(now int64) {
	counters.dbDurationNanos.Add(now)
}

func BeginOutbound(ctx context.Context, now int64) {
	if counters := FromContext(ctx); counters != nil {
		counters.outboundCalls.Add(1)
		counters.outboundNanos.Add(-now)
	}
}

func EndOutbound(ctx context.Context, now, bytes int64) {
	if counters := FromContext(ctx); counters != nil {
		counters.outboundNanos.Add(now)
		if bytes > 0 {
			counters.upstreamBytes.Add(bytes)
		}
	}
}

func (counters *Counters) Snapshot() Snapshot {
	if counters == nil {
		return Snapshot{}
	}
	return Snapshot{
		DBCalls:          nonnegative(counters.dbCalls.Load()),
		DBDuration:       time.Duration(nonnegative(counters.dbDurationNanos.Load())),
		OutboundCalls:    nonnegative(counters.outboundCalls.Load()),
		OutboundDuration: time.Duration(nonnegative(counters.outboundNanos.Load())),
		UpstreamBytes:    nonnegative(counters.upstreamBytes.Load()),
	}
}

func nonnegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
