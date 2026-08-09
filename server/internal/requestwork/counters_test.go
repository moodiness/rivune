package requestwork

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestQueryTracerAggregatesConcurrentDurationsWithFakeClock(t *testing.T) {
	ctx, counters := WithCounters(context.Background())
	now := int64(10)
	tracer := QueryTracer{Now: func() int64 { return now }}

	first := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{})
	now = 20
	second := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{})
	now = 50
	tracer.TraceQueryEnd(first, nil, pgx.TraceQueryEndData{})
	now = 80
	tracer.TraceQueryEnd(second, nil, pgx.TraceQueryEndData{Err: errors.New("query failed")})

	snapshot := counters.Snapshot()
	if snapshot.DBCalls != 2 || snapshot.DBDuration != 100*time.Nanosecond {
		t.Fatalf("database snapshot = %+v", snapshot)
	}
}

func TestCountersAreConcurrentAndRequestLocal(t *testing.T) {
	firstContext, first := WithCounters(context.Background())
	_, second := WithCounters(context.Background())

	const calls = 128
	var group sync.WaitGroup
	group.Add(calls)
	for index := int64(0); index < calls; index++ {
		go func(start int64) {
			defer group.Done()
			BeginDB(firstContext, start)
			BeginOutbound(firstContext, start+1)
			EndOutbound(firstContext, start+8, 11)
			EndDB(firstContext, start+10)
		}(index * 100)
	}
	group.Wait()

	snapshot := first.Snapshot()
	if snapshot.DBCalls != calls || snapshot.DBDuration != calls*10*time.Nanosecond ||
		snapshot.OutboundCalls != calls || snapshot.OutboundDuration != calls*7*time.Nanosecond ||
		snapshot.UpstreamBytes != calls*11 {
		t.Fatalf("concurrent snapshot = %+v", snapshot)
	}
	if isolated := second.Snapshot(); isolated != (Snapshot{}) {
		t.Fatalf("second request was contaminated: %+v", isolated)
	}
	if absent := (*Counters)(nil).Snapshot(); absent != (Snapshot{}) {
		t.Fatalf("absent instrumentation = %+v", absent)
	}
}
